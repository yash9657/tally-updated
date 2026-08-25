import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import {
  ApiError,
  createMockClient,
  type CoredClient,
  type DraftSummary,
  type HttpClient,
  type PurchaseVoucherDraft,
  type VoucherDraftBody,
} from "../../api";
import {
  draftBodyToVoucherRequest,
  resolveDraftLedgers,
  acceptPurchaseDraft,
  acceptEditedPurchaseDraft,
  assertPostableDraft,
  defaultDraftBillRefs,
  draftPartyBillLeg,
  VoucherAcceptFailedError,
  DraftAlreadyAcceptedError,
  DraftBeingPostedError,
  DraftNotPostableError,
  DraftRebuildError,
  INERT_DRAFT_LIFECYCLE,
  insightdDraftLifecycle,
  type DraftLifecyclePort,
  _pageClaimToken,
  _resetAcceptedDraftCache,
  _resetDurableAcceptRecords,
  _resetPendingDraftConfirms,
  _pendingDraftConfirms,
  drainPendingDraftConfirms,
  pendingDraftConfirmCount,
  _setDraftLifecyclePort,
} from "./draftAccept";
import { _resetLedgerCache, _resetOutputTaxLedgerCache } from "./gstVoucher";

const COMPANY = "co-1";

/**
 * A realistic, fully-balanced intra-state @18% purchase draft body: taxable
 * 1000, Input CGST 90 + SGST 90 DEBITed, party CREDIT 1180 = invoice_total.
 * All postings carry real (already-resolved) ledger ids so the PURE converter
 * needs no resolution. dr_cr is the wire "D"/"C".
 */
function balancedBody(): VoucherDraftBody {
  return {
    voucher_class: "purchase",
    party_ledger_id: "ledger-acme",
    party: "Acme Traders",
    voucher_no: "INV-9001",
    voucher_date: "2026-04-02",
    narration: "OCR purchase",
    postings: [
      {
        ledger_id: "ledger-purchases",
        ledger_name: "Purchases",
        dr_cr: "D",
        amount: "1000.0000",
      },
      {
        ledger_id: "ledger-cgst",
        ledger_name: "Input CGST",
        dr_cr: "D",
        amount: "90.0000",
      },
      {
        ledger_id: "ledger-sgst",
        ledger_name: "Input SGST",
        dr_cr: "D",
        amount: "90.0000",
      },
      {
        ledger_id: "ledger-acme",
        ledger_name: "Acme Traders",
        dr_cr: "C",
        amount: "1180.0000",
      },
    ],
    gst: {
      ruleset_version: "gst-2024-09",
      supplier_state_code: "29",
      place_of_supply_state_code: "29",
      reverse_charge: false,
      exempt: false,
      lines: [
        { taxable_value: "1000.0000", gst_rate_pct: "18.0000", hsn: "1234" },
      ],
      cgst_ledger_id: "ledger-cgst",
      sgst_ledger_id: "ledger-sgst",
      igst_ledger_id: "ledger-igst",
    },
  };
}

beforeEach(() => {
  _resetLedgerCache();
  _resetOutputTaxLedgerCache();
  // A FRESH PAGE. The session memo is page-lifetime, so clearing it between tests is
  // what makes "this survives a reload" a meaningful assertion below.
  _resetAcceptedDraftCache();
  // A FRESH MACHINE. The durable memo (localStorage) deliberately OUTLIVES a reload,
  // so it must be cleared per test or one test's accept refuses the next one's.
  _resetDurableAcceptRecords();
  // §7.3 B's queue is localStorage too, so it outlives a test the same way the
  // memo does — and an owed confirm left behind would silently drain into the
  // NEXT test's fake port.
  _resetPendingDraftConfirms();
  // ⛔ LAYER 4 OFF BY DEFAULT IN THESE TESTS, ON PURPOSE. The real port talks to
  // insightd on another port; leaving it live here would make every accept in this
  // file depend on whether a dev insightd happens to be running. Tests that are
  // ABOUT layer 4 install their own fake below.
  _setDraftLifecyclePort(INERT_DRAFT_LIFECYCLE);
});
afterEach(() => {
  vi.restoreAllMocks();
  _setDraftLifecyclePort(null);
  _resetDurableAcceptRecords();
  _resetPendingDraftConfirms();
});

describe("draftBodyToVoucherRequest (pure)", () => {
  it("drops ledger_name + voucher_class and keeps dr_cr/amount verbatim", () => {
    const req = draftBodyToVoucherRequest(balancedBody());
    expect(req).not.toHaveProperty("voucher_class");
    expect(req.postings).toEqual([
      { ledger_id: "ledger-purchases", dr_cr: "D", amount: "1000.0000" },
      { ledger_id: "ledger-cgst", dr_cr: "D", amount: "90.0000" },
      { ledger_id: "ledger-sgst", dr_cr: "D", amount: "90.0000" },
      { ledger_id: "ledger-acme", dr_cr: "C", amount: "1180.0000" },
    ]);
    // Header scalars carried through.
    expect(req.voucher_no).toBe("INV-9001");
    expect(req.voucher_date).toBe("2026-04-02");
    expect(req.narration).toBe("OCR purchase");
    expect(req.party).toBe("Acme Traders");
    expect(req.party_ledger_id).toBe("ledger-acme");
  });

  it("balances: sum(debit) === sum(credit) on the converted postings", () => {
    const req = draftBodyToVoucherRequest(balancedBody());
    const sum = (dc: "D" | "C") =>
      (req.postings ?? [])
        .filter((p) => p.dr_cr === dc)
        .reduce((a, p) => a + parseFloat(p.amount), 0);
    expect(sum("D")).toBe(1180);
    expect(sum("C")).toBe(1180);
  });

  it("maps gst incl. exempt + ruleset_version, carries the 3 ledger ids, null->undefined", () => {
    const req = draftBodyToVoucherRequest(balancedBody());
    expect(req.gst).toEqual({
      ruleset_version: "gst-2024-09",
      reverse_charge: false,
      exempt: false,
      supplier_state_code: "29",
      place_of_supply_state_code: "29",
      lines: [
        { taxable_value: "1000.0000", gst_rate_pct: "18.0000", hsn: "1234" },
      ],
      cgst_ledger_id: "ledger-cgst",
      sgst_ledger_id: "ledger-sgst",
      igst_ledger_id: "ledger-igst",
    });
  });

  it("omits null/empty header scalars and null gst optionals", () => {
    const body = balancedBody();
    body.voucher_no = null;
    body.narration = null;
    body.gst!.supplier_state_code = null;
    body.gst!.cgst_ledger_id = null;
    const req = draftBodyToVoucherRequest(body);
    expect(req).not.toHaveProperty("voucher_no");
    expect(req).not.toHaveProperty("narration");
    expect(req.gst).not.toHaveProperty("supplier_state_code");
    expect(req.gst).not.toHaveProperty("cgst_ledger_id");
  });

  it("throws when any posting still has a null ledger_id", () => {
    const body = balancedBody();
    body.postings[0]!.ledger_id = null;
    expect(() => draftBodyToVoucherRequest(body)).toThrow(/null ledger_id/);
  });
});

describe("resolveDraftLedgers (async)", () => {
  it("fills null posting ids and patches the party credit posting id", async () => {
    const client = createMockClient({ seed: false });
    const body = balancedBody();
    // Unresolved supplier (Alt+C) + an unresolved expense master.
    body.party_ledger_id = null;
    body.postings[0]!.ledger_id = null; // Purchases (expense) -> Purchase Accounts
    body.postings[3]!.ledger_id = null; // party credit -> resolved party id

    const resolved = await resolveDraftLedgers(client, COMPANY, body);

    // Every id is now filled.
    expect(resolved.party_ledger_id).toBeTruthy();
    expect(resolved.postings.every((p) => p.ledger_id != null)).toBe(true);
    // The party credit posting carries the SAME id as party_ledger_id.
    expect(resolved.postings[3]!.ledger_id).toBe(resolved.party_ledger_id);
    // Distinct ledgers were minted for party vs expense.
    expect(resolved.postings[0]!.ledger_id).not.toBe(resolved.party_ledger_id);
    // Already-resolved tax postings are untouched.
    expect(resolved.postings[1]!.ledger_id).toBe("ledger-cgst");
  });

  it("creates the party under Sundry Creditors and the expense under Purchase Accounts", async () => {
    const client = createMockClient({ seed: false });
    const spy = vi.spyOn(client, "createLedger");
    const body = balancedBody();
    body.party_ledger_id = null;
    body.postings[0]!.ledger_id = null;
    body.postings[3]!.ledger_id = null;

    await resolveDraftLedgers(client, COMPANY, body);

    const groups = spy.mock.calls.map((c) => ({
      name: c[1].name,
      group: c[1].group_name,
    }));
    expect(groups).toContainEqual({
      name: "Acme Traders",
      group: "Sundry Creditors",
    });
    expect(groups).toContainEqual({
      name: "Purchases",
      group: "Purchase Accounts",
    });
  });

  it("falls back to the single CREDIT posting when no name matches the party", async () => {
    const client = createMockClient({ seed: false });
    const body = balancedBody();
    body.party = "Renamed Supplier"; // no posting ledger_name matches
    body.party_ledger_id = null;
    body.postings[3]!.ledger_id = null; // the lone credit leg

    const resolved = await resolveDraftLedgers(client, COMPANY, body);
    expect(resolved.postings[3]!.ledger_id).toBe(resolved.party_ledger_id);
  });
});

describe("acceptPurchaseDraft (the only write path)", () => {
  it("posts the converted body via postPurchaseVoucher exactly once", async () => {
    const client: CoredClient = createMockClient({ seed: false });
    const spy = vi.spyOn(client, "postPurchaseVoucher");

    const res = await acceptPurchaseDraft(client, COMPANY, balancedBody());

    expect(spy).toHaveBeenCalledTimes(1);
    // The posted body equals the converted body (balanced, fixed-4 strings).
    const [, postedReq] = spy.mock.calls[0]!;
    expect(postedReq).toEqual(draftBodyToVoucherRequest(balancedBody()));

    // The mock echoes the intra @18 split from gst.lines: 90 / 90 / 0.
    expect(res.cgst).toBe("90.0000");
    expect(res.sgst).toBe("90.0000");
    expect(res.igst).toBe("0.0000");
    expect(res.voucher_no).toBe("INV-9001");
  });

  it("resolves null ledgers first, then posts a fully-balanced request", async () => {
    const client = createMockClient({ seed: false });
    const spy = vi.spyOn(client, "postPurchaseVoucher");
    const body = balancedBody();
    body.party_ledger_id = null;
    body.postings[3]!.ledger_id = null;

    await acceptPurchaseDraft(client, COMPANY, body);

    expect(spy).toHaveBeenCalledTimes(1);
    const [, postedReq] = spy.mock.calls[0]!;
    // No null ids leaked into the POST.
    expect((postedReq.postings ?? []).every((p) => !!p.ledger_id)).toBe(true);
    const sum = (dc: string) =>
      (postedReq.postings ?? [])
        .filter((p) => p.dr_cr === dc)
        .reduce((a, p) => a + parseFloat(p.amount), 0);
    expect(sum("D")).toBe(sum("C"));
  });
});

// ===========================================================================
// THE THREE DEFECTS DRIVEN IN A BROWSER ON THE BILL INBOX (row 30)
//
// These use a REAL mock company: the four ledgers are minted through the client
// so the chart the accept path reads actually contains them. That matters —
// `defaultDraftBillRefs` reads the party's own `maintain_bill_by_bill` flag from
// that chart, and the day-book duplicate check reads the vouchers it posted.
// ===========================================================================

/**
 * Mint the draft's four ledgers for real and return a body wired to their ids.
 * `billByBill` is the party's `maintain_bill_by_bill` — the SAME flag
 * VoucherScreen's panel render condition reads, so `true` is exactly the case in
 * which the operator was shown "referenced in full (1180.0000)".
 */
async function realDraftBody(
  client: CoredClient,
  billByBill: boolean,
): Promise<VoucherDraftBody> {
  /**
   * ⛔⛔ RESOLVE-OR-CREATE, NOT CREATE. THIS HELPER CALLED `createLedger`
   * UNCONDITIONALLY AND D84's TIGHTENED MOCK IS WHAT EXPOSED IT.
   *
   * A test calls this twice in one company whenever it builds two draft bodies
   * (the "genuinely different bill from the same supplier" cases do exactly
   * that), and the second call re-created "Acme Traders". The old mock accepted
   * it and pushed a SECOND ledger row with the same name; cored answers
   * `422 masters: this company already has a ledger named "Acme Traders"`.
   *
   * ⚠️ SO FIVE TESTS IN THIS FILE COULD NEVER HAVE RUN AGAINST THE REAL SERVER,
   * and the ones that "passed" were measuring a chart of accounts with DUPLICATE
   * party ledgers in it — the exact state the product's own `ensureLedger` was
   * fixed TWICE to avoid (see gstVoucher.ts). The helper now does what the
   * product does: resolve first, create only if absent.
   */
  const ensure = async (req: {
    name: string;
    group_name: string;
    state_code?: string;
    maintain_bill_by_bill?: boolean;
  }): Promise<{ ledger_id: string }> => {
    const existing = (await client.listLedgers(COMPANY)).ledgers.find(
      (l) => l.name === req.name,
    );
    if (existing) return { ledger_id: existing.id };
    return await client.createLedger(COMPANY, req as never);
  };
  const party = await ensure({
    name: "Acme Traders",
    group_name: "Sundry Creditors",
    state_code: "29",
    maintain_bill_by_bill: billByBill,
  });
  const purchases = await ensure({
    name: "Purchases",
    group_name: "Purchase Accounts",
  });
  const cgst = await ensure({
    name: "Input CGST",
    group_name: "Duties & Taxes",
  });
  const sgst = await ensure({
    name: "Input SGST",
    group_name: "Duties & Taxes",
  });
  const body = balancedBody();
  body.party_ledger_id = party.ledger_id;
  body.postings[0]!.ledger_id = purchases.ledger_id;
  body.postings[1]!.ledger_id = cgst.ledger_id;
  body.postings[2]!.ledger_id = sgst.ledger_id;
  body.postings[3]!.ledger_id = party.ledger_id;
  body.gst!.cgst_ledger_id = cgst.ledger_id;
  body.gst!.sgst_ledger_id = sgst.ledger_id;
  body.gst!.igst_ledger_id = null;
  return body;
}

describe("DEFECT 2 — the bill-wise references the panel certifies reach the wire", () => {
  it("posts ONE NEW reference for the whole party leg, and the payable reaches Outstandings", async () => {
    const client = createMockClient({ seed: false });
    const body = await realDraftBody(client, true);
    const spy = vi.spyOn(client, "postPurchaseVoucher");

    const res = await acceptPurchaseDraft(client, COMPANY, body);

    // THE WIRE. Independent literals: 1180.0000 is the invoice total the panel
    // certifies as "referenced in full", credited (a purchase credits the
    // creditor), and the NAME IS EMPTY because post() stamps the number it
    // allocates inside its own transaction.
    const [, req] = spy.mock.calls[0]!;
    expect(req.bill_allocations).toEqual([
      {
        ledger_id: body.party_ledger_id,
        ref_name: "",
        ref_type: "NEW",
        dr_cr: "C",
        amount: "1180.0000",
      },
    ]);

    // THE BOOKS. The stored reference carries the ALLOCATED number, and — the
    // thing the defect broke — the payable is now IN the Outstandings report
    // instead of sitting only in Sundry Creditors on the Balance Sheet.
    const view = await client.getVoucher(COMPANY, res.voucher_id);
    expect(view.bill_allocations).toEqual([
      {
        ledger_id: body.party_ledger_id,
        ref_name: "INV-9001",
        ref_type: "NEW",
        dr_cr: "C",
        amount: "1180.0000",
        due_date: "",
      },
    ]);
    const out = await client.getOutstandings(COMPANY, { kind: "PAYABLE" });
    expect(out.parties).toHaveLength(1);
    expect(out.parties[0]!.ledger).toBe("Acme Traders");
    expect(out.parties[0]!.pending).toBe("1180.0000");
    expect(out.parties[0]!.bills.map((b) => b.ref_name)).toEqual(["INV-9001"]);
  });

  it("sends NO references for a party that does not maintain bill-by-bill", async () => {
    const client = createMockClient({ seed: false });
    const body = await realDraftBody(client, false);
    const spy = vi.spyOn(client, "postPurchaseVoucher");

    await acceptPurchaseDraft(client, COMPANY, body);

    // The panel does not render for such a party, so nothing was certified and
    // nothing may be invented. Omitted, never `[]`.
    const [, req] = spy.mock.calls[0]!;
    expect(req).not.toHaveProperty("bill_allocations");
  });

  it("carries the OPERATOR's references when the caller supplies them", async () => {
    const client = createMockClient({ seed: false });
    const body = await realDraftBody(client, true);
    const spy = vi.spyOn(client, "postPurchaseVoucher");

    // A split the operator keyed: 1000 opens the bill, 180 goes on account. It
    // totals the party leg EXACTLY, which is what the server (and the mock)
    // enforce to the paisa.
    await acceptPurchaseDraft(client, COMPANY, body, {
      billRefs: [
        {
          refType: "NEW",
          refName: "",
          amount: "1000.0000",
          dueDate: "2026-05-02",
        },
        { refType: "ON_ACCOUNT", refName: "", amount: "180.0000", dueDate: "" },
      ],
    });

    const [, req] = spy.mock.calls[0]!;
    expect(req.bill_allocations).toEqual([
      {
        ledger_id: body.party_ledger_id,
        ref_name: "",
        ref_type: "NEW",
        dr_cr: "C",
        amount: "1000.0000",
        due_date: "2026-05-02",
      },
      {
        ledger_id: body.party_ledger_id,
        ref_name: "",
        ref_type: "ON_ACCOUNT",
        dr_cr: "C",
        amount: "180.0000",
      },
    ]);
  });

  it("draftPartyBillLeg nets the party's postings, signed, and is null when there is nothing to reference", () => {
    const body = balancedBody();
    // The party is credited 1180 -> referenced on the CREDIT side for 1180.
    expect(draftPartyBillLeg(body)).toEqual({
      ledgerId: "ledger-acme",
      drCr: "C",
      amount: "1180.0000",
    });
    // A party that nets to zero (a contra pair) is not a bill.
    const zeroed = balancedBody();
    zeroed.postings.push({
      ledger_id: "ledger-acme",
      ledger_name: "Acme Traders",
      dr_cr: "D",
      amount: "1180.0000",
    });
    expect(draftPartyBillLeg(zeroed)).toBeNull();
    // No party ledger yet (pre-resolution) -> nothing to attach a reference to.
    const unresolved = balancedBody();
    unresolved.party_ledger_id = null;
    expect(draftPartyBillLeg(unresolved)).toBeNull();
  });

  it("defaultDraftBillRefs gates on the party's own flag, not on the draft", () => {
    const body = balancedBody();
    const chart = (flag: boolean) => [
      {
        id: "ledger-acme",
        name: "Acme Traders",
        group_name: "Sundry Creditors",
        maintain_bill_by_bill: flag,
      } as unknown as Parameters<typeof defaultDraftBillRefs>[1][number],
    ];
    expect(defaultDraftBillRefs(body, chart(true))).toEqual([
      {
        refType: "NEW",
        refName: "",
        amount: "1180.0000",
        dueDate: "",
        auto: true,
      },
    ]);
    expect(defaultDraftBillRefs(body, chart(false))).toEqual([]);
    // A party the chart does not carry (minted by this very accept) has no flag,
    // so it gets no reference.
    expect(defaultDraftBillRefs(body, [])).toEqual([]);
  });
});

describe("DEFECT 1 — a draft can be accepted only once", () => {
  it("refuses a SECOND accept of the same draft and posts no second voucher", async () => {
    const client = createMockClient({ seed: false });
    const body = await realDraftBody(client, true);
    const spy = vi.spyOn(client, "postPurchaseVoucher");

    await acceptPurchaseDraft(client, COMPANY, body);
    await expect(
      acceptPurchaseDraft(client, COMPANY, body),
    ).rejects.toBeInstanceOf(DraftAlreadyAcceptedError);

    // THE MONEY ASSERTION: one payable, not two.
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("still refuses AFTER A PAGE RELOAD, because the books are asked too", async () => {
    const client = createMockClient({ seed: false });
    const body = await realDraftBody(client, true);
    const spy = vi.spyOn(client, "postPurchaseVoucher");
    const first = await acceptPurchaseDraft(client, COMPANY, body);

    // A RELOAD: every page-lifetime memo is gone, and the Bill Inbox still shows
    // the draft as "ready" because insightd has no route that would mark it
    // otherwise. Re-opening it hands the screen a FRESH body object.
    _resetAcceptedDraftCache();
    _resetLedgerCache();
    const reopened = await realDraftBody(client, true);
    reopened.party_ledger_id = body.party_ledger_id;
    reopened.postings = body.postings.map((p) => ({ ...p }));

    const err = await acceptPurchaseDraft(client, COMPANY, reopened).then(
      () => null,
      (e: unknown) => e,
    );
    expect(err).toBeInstanceOf(DraftAlreadyAcceptedError);
    // It names the voucher already on the books, so the operator can go and look.
    expect((err as DraftAlreadyAcceptedError).voucherNo).toBe("INV-9001");
    expect((err as DraftAlreadyAcceptedError).voucherID).toBe(first.voucher_id);
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("treats a DOUBLE-PRESS in one tick as one accept (same voucher, one post)", async () => {
    const client = createMockClient({ seed: false });
    const body = await realDraftBody(client, true);
    const spy = vi.spyOn(client, "postPurchaseVoucher");

    const [a, b] = await Promise.all([
      acceptPurchaseDraft(client, COMPANY, body),
      acceptPurchaseDraft(client, COMPANY, body),
    ]);

    expect(spy).toHaveBeenCalledTimes(1);
    expect(a.voucher_id).toBe(b.voucher_id);
  });

  it("keys on the PRISTINE draft, so editing a field does not buy a second post", async () => {
    const client = createMockClient({ seed: false });
    const body = await realDraftBody(client, true);
    // No supplier invoice number: the day-book layer has nothing to match on, so
    // this test isolates the session memo — and proves it keys on the pristine
    // body rather than on what is posted.
    body.voucher_no = null;
    const spy = vi.spyOn(client, "postPurchaseVoucher");

    await acceptPurchaseDraft(client, COMPANY, body);
    await expect(
      acceptEditedPurchaseDraft(client, COMPANY, body, {
        narration: "Edited after accepting",
      }),
    ).rejects.toBeInstanceOf(DraftAlreadyAcceptedError);

    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("REJECTS (never throws synchronously) when the edit itself is refused, and does not poison the memo", async () => {
    const client = createMockClient({ seed: false });
    const body = await realDraftBody(client, true);
    body.gst!.reverse_charge = true; // not rebuildable -> applyDraftEdits throws
    const spy = vi.spyOn(client, "postPurchaseVoucher");

    // A SYNCHRONOUS throw here would skip VoucherScreen's `.finally`, leaving its
    // in-flight latch stuck and Ctrl+A dead for the rest of the session.
    let sync = false;
    let promise: Promise<unknown> | undefined;
    try {
      promise = acceptEditedPurchaseDraft(client, COMPANY, body, {
        taxableValue: "2000",
      });
    } catch {
      sync = true;
    }
    expect(sync).toBe(false);
    await expect(promise).rejects.toBeInstanceOf(DraftRebuildError);
    expect(spy).not.toHaveBeenCalled();

    // The refused attempt left NOTHING behind: a pristine accept of the same
    // draft still works, and is not mistaken for a second accept.
    const res = await acceptPurchaseDraft(client, COMPANY, body);
    expect(res.voucher_no).toBe("INV-9001");
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("FORGETS a failed accept, so the operator can fix it and retry", async () => {
    const client = createMockClient({ seed: false });
    const body = await realDraftBody(client, true);
    const spy = vi
      .spyOn(client, "postPurchaseVoucher")
      .mockRejectedValueOnce(new Error("422: back-dated beyond the cutoff"));

    await expect(acceptPurchaseDraft(client, COMPANY, body)).rejects.toThrow(
      /back-dated/,
    );
    // The retry is NOT refused as a duplicate — nothing was posted the first time.
    const res = await acceptPurchaseDraft(client, COMPANY, body);
    expect(res.voucher_no).toBe("INV-9001");
    expect(spy).toHaveBeenCalledTimes(2);
  });
});

describe("DEFECT 3 — a refused accept must not quietly leave masters behind", () => {
  it("names the ledgers it created when the voucher POST is refused", async () => {
    const client = createMockClient({ seed: false });
    const body = balancedBody();
    // An Alt+C supplier and an Alt+C expense master: both are created before the
    // POST, because the voucher needs their ids.
    body.party_ledger_id = null;
    body.postings[0]!.ledger_id = null;
    body.postings[3]!.ledger_id = null;
    const created = vi.spyOn(client, "createLedger");
    vi.spyOn(client, "postPurchaseVoucher").mockRejectedValue(
      new Error("422: ledger: voucher date is before the books cutoff"),
    );

    const err = await acceptPurchaseDraft(client, COMPANY, body).then(
      () => null,
      (e: unknown) => e,
    );

    expect(err).toBeInstanceOf(VoucherAcceptFailedError);
    const failed = err as VoucherAcceptFailedError;
    // The server's own refusal comes FIRST and unaltered — it is what the
    // operator needs in order to fix the voucher.
    expect(failed.message).toMatch(/^422: ledger: voucher date is before/);
    // …and then the orphans are named, because there is no route that deletes a
    // ledger and an invisible orphan is the defect.
    expect(failed.createdLedgers.map((l) => l.name).sort()).toEqual([
      "Acme Traders",
      "Purchases",
    ]);
    expect(failed.message).toContain("Acme Traders");
    expect(failed.message).toContain("Retrying this accept REUSES");
    // Honest about what actually happened: they WERE created.
    expect(created).toHaveBeenCalledTimes(2);
  });

  it("does not wrap the failure when this accept created nothing", async () => {
    const client = createMockClient({ seed: false });
    const body = await realDraftBody(client, true);
    vi.spyOn(client, "postPurchaseVoucher").mockRejectedValue(
      new Error("422: GST verification failed"),
    );

    await expect(acceptPurchaseDraft(client, COMPANY, body)).rejects.toThrow(
      "422: GST verification failed",
    );
    await expect(
      acceptPurchaseDraft(client, COMPANY, body),
    ).rejects.not.toBeInstanceOf(VoucherAcceptFailedError);
  });

  it("refuses an unpostable draft BEFORE creating a single master", async () => {
    const client = createMockClient({ seed: false });
    const created = vi.spyOn(client, "createLedger");
    const posted = vi.spyOn(client, "postPurchaseVoucher");

    const unbalanced = balancedBody();
    unbalanced.party_ledger_id = null; // would be created first, under the old order
    unbalanced.postings[3]!.amount = "1000.0000"; // 1180 Dr vs 1000 Cr
    await expect(
      acceptPurchaseDraft(client, COMPANY, unbalanced),
    ).rejects.toBeInstanceOf(DraftNotPostableError);

    // The "processing" placeholder insightd stores while real OCR runs.
    const placeholder: VoucherDraftBody = {
      voucher_class: "purchase",
      party: "Acme Traders",
      party_ledger_id: null,
      postings: [],
    };
    await expect(
      acceptPurchaseDraft(client, COMPANY, placeholder),
    ).rejects.toBeInstanceOf(DraftNotPostableError);

    expect(created).not.toHaveBeenCalled();
    expect(posted).not.toHaveBeenCalled();
  });

  it("assertPostableDraft states the imbalance in fixed-4, never a float", () => {
    const body = balancedBody();
    body.postings[1]!.amount = "90.0050";
    expect(() => assertPostableDraft(body)).toThrow(/0\.0050/);
    expect(() => assertPostableDraft(balancedBody())).not.toThrow();
  });
});

// ===========================================================================
// DEFECT 1, ROUND TWO — THE BROWSER RE-DRIVE THAT PROVED THE FIRST FIX
// INSUFFICIENT, AND EVERY OTHER DOOR TO THE SAME BEHAVIOUR
//
// After round 1 (session memo + day-book lookup) a purchase draft whose invoice
// number the OCR never read was accepted, THE PAGE WAS RELOADED, and it was
// accepted again — TWO REAL VOUCHERS for one supplier bill:
//
//   voucher_no | voucher_date | taxable_value | igst
//   3          | 2026-07-12   | 5000.0000     | 900.0000
//   4          | 2026-07-12   | 5000.0000     | 900.0000   <- the same bill, twice
//
// and the inbox row STILL read "Pune Fabricators · 21240.0000 · ready · 95%"
// afterwards, which is the root cause: nothing marked the draft consumed.
//
// These tests drive each layer INDEPENDENTLY — with the others switched off — so
// that no one of them can be silently carrying the others. `_resetAcceptedDraftCache`
// is a page RELOAD; `_resetDurableAcceptRecords` is a different MACHINE.
// ===========================================================================

/** A numberless purchase draft: the exact shape the re-drive duplicated. */
async function numberlessDraftBody(
  client: CoredClient,
): Promise<VoucherDraftBody> {
  const body = await realDraftBody(client, true);
  body.voucher_no = null; // the OCR never read it -> the day book has no key
  return body;
}

/** Let the un-awaited insightd bookkeeping in recordAcceptance settle. */
const flush = () => new Promise((r) => setTimeout(r, 0));

/**
 * A lifecycle port overriding only the methods a test cares about, with the INERT
 * (no-opinion) behaviour for the rest.
 *
 * ⚠️ Deliberately spread over INERT rather than typed as a partial: `claim` and
 * `releaseClaim` are REQUIRED members of {@link DraftLifecyclePort}, so a port that
 * forgot them would not compile — which is what stops layer 5 being quietly dropped by
 * a future implementation, the way the whole mechanism was quietly unwired before.
 */
function portWith(overrides: Partial<DraftLifecyclePort>): DraftLifecyclePort {
  return { ...INERT_DRAFT_LIFECYCLE, ...overrides };
}

/**
 * A fake insightd: the draft store, the two GET routes and the THREE POST routes,
 * behaving as insightd/insightd/api/routes_drafts.py does (including the terminal
 * 409 on a second acceptance). Used to drive `insightdDraftLifecycle` FOR REAL —
 * the body-match resolution and the URLs it POSTs are the code under test, not a
 * stub, because that resolution is what makes layer 4 reachable from the F9 screen
 * (which does not pass a draft id).
 *
 * ⛔ THE CLAIM ROUTES ARE MODELLED, NOT STUBBED, AND THAT IS THE POINT OF THE
 * CONCURRENCY TESTS. `/claim` here is the same compare-and-set the server performs
 * under `SELECT … FOR UPDATE` (insightd/api/store.py `decide_claim`): a live claim held
 * by a DIFFERENT token is refused 409 `draft_being_posted`, the SAME token renews, a
 * lapsed lease is re-claimable, and `/claim/release` refuses a token that is not the
 * holder. A fake that always said "claimed" would let the client's claim be wired to
 * nothing and still pass — which is exactly the failure mode this round exists to
 * avoid.
 */
function fakeInsightd(bodies: VoucherDraftBody[]) {
  const rows = bodies.map((body, i) => ({
    draft_id: `D-${i + 1}`,
    company_id: COMPANY,
    created_at: "2026-07-12T04:00:00+00:00",
    source: "upload" as const,
    status: "ready",
    body,
    accepted_voucher_id: null as string | null,
    accepted_voucher_no: null as string | null,
    // The lease. null token = unclaimed; an expiry in the past = lapsed, which reads
    // as unclaimed (a crashed browser must not strand the bill forever).
    claim_token: null as string | null,
    claimed_by: null as string | null,
    claim_expires_at: null as string | null,
  }));
  const posts: { path: string; body: unknown }[] = [];
  /** The claim TTL the fake enforces (the server's is CLAIM_TTL_SECONDS = 120). */
  const TTL_MS = 120_000;
  const liveClaim = (r: (typeof rows)[number]): string | null => {
    if (!r.claim_token || !r.claim_expires_at) return null;
    return Date.parse(r.claim_expires_at) > Date.now() ? r.claim_token : null;
  };

  const insightd = {
    listDrafts: (companyID: string) =>
      Promise.resolve(
        rows
          .filter((r) => r.company_id === companyID)
          .map((r) => ({
            draft_id: r.draft_id,
            company_id: r.company_id,
            created_at: r.created_at,
            source: r.source,
            status: r.status,
            party: r.body.party ?? "",
            grand_total: "0.0000",
            confidence: 0.95,
            accepted_voucher_no: r.accepted_voucher_no,
            accepted_voucher_id: r.accepted_voucher_id,
          })) as unknown as DraftSummary[],
      ),
    getDraft: (companyID: string, draftID: string) => {
      const row = rows.find(
        (r) => r.company_id === companyID && r.draft_id === draftID,
      );
      if (!row) return Promise.reject(new ApiError(404, "draft not found"));
      return Promise.resolve(row as unknown as PurchaseVoucherDraft);
    },
  };

  const alreadyAccepted = (row: (typeof rows)[number]) =>
    Promise.reject(
      new ApiError(409, "already accepted", {
        detail: {
          code: "draft_already_accepted",
          accepted_voucher_id: row.accepted_voucher_id,
          accepted_voucher_no: row.accepted_voucher_no,
        },
      }),
    );

  const http = {
    post: (path: string, body?: unknown) => {
      posts.push({ path, body: body ?? null });
      const tail = path.split("/drafts/")[1]!;
      const id = decodeURIComponent(tail.split("/")[0]!);
      const route = tail.slice(tail.indexOf("/") + 1); // "claim" | "claim/release" | "accepted"
      const row = rows.find((r) => r.draft_id === id);
      if (!row) return Promise.reject(new ApiError(404, "draft not found"));

      if (route === "claim") {
        const { claim_token: token, claimed_by: by } = body as {
          claim_token: string;
          claimed_by: string;
        };
        if (row.status === "accepted") return alreadyAccepted(row);
        if (row.status === "processing") {
          return Promise.reject(
            new ApiError(409, "still processing", {
              detail: { code: "draft_still_processing" },
            }),
          );
        }
        const held = liveClaim(row);
        if (held !== null && held !== token) {
          return Promise.reject(
            new ApiError(409, "draft being posted", {
              detail: {
                code: "draft_being_posted",
                claimed_by: row.claimed_by,
                claim_expires_at: row.claim_expires_at,
              },
            }),
          );
        }
        row.claim_token = token;
        row.claimed_by = by;
        row.claim_expires_at = new Date(Date.now() + TTL_MS).toISOString();
        return Promise.resolve(row);
      }

      if (route === "claim/release") {
        const { claim_token: token } = body as { claim_token: string };
        if (row.status === "accepted") return alreadyAccepted(row);
        const held = liveClaim(row);
        if (held === null) return Promise.resolve(row); // idempotent success
        if (held !== token) {
          return Promise.reject(
            new ApiError(409, "claimed by another", {
              detail: {
                code: "draft_claimed_by_another",
                claim_expires_at: row.claim_expires_at,
              },
            }),
          );
        }
        row.claim_token = null;
        row.claimed_by = null;
        row.claim_expires_at = null;
        return Promise.resolve(row);
      }

      // "accepted": the terminal CAS. A confirm is never refused for a lapsed claim.
      if (row.status === "accepted") return alreadyAccepted(row);
      const v = body as { voucher_id: string; voucher_no: string };
      row.status = "accepted";
      row.accepted_voucher_id = v.voucher_id;
      row.accepted_voucher_no = v.voucher_no;
      row.claim_token = null;
      row.claimed_by = null;
      row.claim_expires_at = null;
      return Promise.resolve(row);
    },
  } as unknown as HttpClient;

  /** Force every live lease to have lapsed (the crashed-browser / TTL case). */
  const lapseClaims = () => {
    for (const r of rows) {
      if (r.claim_token)
        r.claim_expires_at = new Date(Date.now() - 1).toISOString();
    }
  };

  return {
    rows,
    posts,
    lapseClaims,
    port: insightdDraftLifecycle({ insightd, http }),
  };
}

describe("LAYER 3 — the durable memo, which is what a page reload used to defeat", () => {
  it("⛔ refuses a NUMBERLESS draft accepted, RELOADED and accepted again", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const spy = vi.spyOn(client, "postPurchaseVoucher");

    const first = await acceptPurchaseDraft(client, COMPANY, body);

    // THE RELOAD. Every page-lifetime memo is gone; the Bill Inbox hands the screen
    // a FRESH body object for the same draft. Layer 2 has nothing to match on (no
    // invoice number) and layer 4 is switched off in this test, so ONLY the durable
    // memo can refuse this — which is the point.
    _resetAcceptedDraftCache();
    _resetLedgerCache();
    const reopened = await numberlessDraftBody(client);
    reopened.party_ledger_id = body.party_ledger_id;
    reopened.postings = body.postings.map((p) => ({ ...p }));

    const err = await acceptPurchaseDraft(client, COMPANY, reopened).then(
      () => null,
      (e: unknown) => e,
    );

    expect(err).toBeInstanceOf(DraftAlreadyAcceptedError);
    // It names the voucher the FIRST accept produced, so the operator can go look.
    expect((err as DraftAlreadyAcceptedError).voucherID).toBe(first.voucher_id);
    expect((err as DraftAlreadyAcceptedError).message).toMatch(
      /already accepted/i,
    );
    // ⛔ THE MONEY ASSERTION: one payable and one lot of input tax, not two.
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("refuses across a reload for an EDITED re-accept too (keyed on the pristine draft)", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const spy = vi.spyOn(client, "postPurchaseVoucher");
    await acceptPurchaseDraft(client, COMPANY, body);

    _resetAcceptedDraftCache();
    _resetLedgerCache();
    await expect(
      acceptEditedPurchaseDraft(client, COMPANY, body, {
        narration: "Edited after a reload",
        taxableValue: "1000",
      }),
    ).rejects.toBeInstanceOf(DraftAlreadyAcceptedError);
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("does NOT refuse a genuinely different bill from the same supplier", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const spy = vi.spyOn(client, "postPurchaseVoucher");
    await acceptPurchaseDraft(client, COMPANY, body);

    // A second, real bill: same supplier, same date, DIFFERENT amounts. The durable
    // key is the draft's content, so this is a different key and must go through —
    // a guard that refused it would be worse than the defect it prevents.
    _resetAcceptedDraftCache();
    const other = await numberlessDraftBody(client);
    other.postings[0]!.amount = "2000.0000";
    other.postings[1]!.amount = "180.0000";
    other.postings[2]!.amount = "180.0000";
    other.postings[3]!.amount = "2360.0000";
    other.gst!.lines[0]!.taxable_value = "2000.0000";

    await expect(
      acceptPurchaseDraft(client, COMPANY, other),
    ).resolves.toBeTruthy();
    expect(spy).toHaveBeenCalledTimes(2);
  });

  it("a failed accept leaves NOTHING durable behind, so the operator can retry", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const spy = vi
      .spyOn(client, "postPurchaseVoucher")
      .mockRejectedValueOnce(new Error("422: back-dated beyond the cutoff"));

    await expect(acceptPurchaseDraft(client, COMPANY, body)).rejects.toThrow(
      /422/,
    );
    // …even across a reload: a memo that remembered a FAILURE would lock the
    // operator out of their own bill forever, not just for this page.
    _resetAcceptedDraftCache();
    await expect(
      acceptPurchaseDraft(client, COMPANY, body),
    ).resolves.toBeTruthy();
    expect(spy).toHaveBeenCalledTimes(2);
  });

  it("survives an unusable localStorage instead of breaking the accept", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    // Safari private mode / a full quota: every access throws.
    vi.spyOn(globalThis.localStorage, "setItem").mockImplementation(() => {
      throw new Error("QuotaExceededError");
    });
    vi.spyOn(globalThis.localStorage, "getItem").mockImplementation(() => {
      throw new Error("SecurityError");
    });
    // The accept still succeeds. Losing the memo costs a duplicate-detection
    // chance; throwing here would cost the operator their voucher.
    await expect(
      acceptPurchaseDraft(client, COMPANY, body),
    ).resolves.toBeTruthy();
  });

  it("⚠️ THE RESIDUAL, STATED: a brand-new machine with insightd unaware is NOT covered", async () => {
    // Layers 1 and 3 cleared (a different browser profile) and layer 4 inert
    // (insightd knows nothing of this draft). A numberless bill has no key for
    // layer 2 either, so the duplicate goes through. This is exactly why layer 4 —
    // the SERVER-side terminal state — is the root fix and not an optional extra;
    // asserting it here keeps the honest limit of the client-side layers visible
    // rather than implied.
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const spy = vi.spyOn(client, "postPurchaseVoucher");
    await acceptPurchaseDraft(client, COMPANY, body);

    _resetAcceptedDraftCache();
    _resetDurableAcceptRecords();
    await expect(
      acceptPurchaseDraft(client, COMPANY, body),
    ).resolves.toBeTruthy();
    expect(spy).toHaveBeenCalledTimes(2);
  });
});

describe("LAYER 4 — insightd's terminal draft state (the root fix)", () => {
  it("⛔ refuses a numberless draft on a MACHINE THAT HAS NEVER SEEN IT, because the server remembers", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    _setDraftLifecyclePort(fake.port);
    const spy = vi.spyOn(client, "postPurchaseVoucher");

    const first = await acceptPurchaseDraft(client, COMPANY, body);
    await flush();

    // The SERVER now records the acceptance — resolved BY BODY, because the F9
    // screen does not pass a draft id. ⛔ TWO posts, in THIS order: the draft is
    // RESERVED first and confirmed second.
    expect(fake.posts.map((p) => p.path)).toEqual([
      `/v1/companies/${COMPANY}/drafts/D-1/claim`,
      `/v1/companies/${COMPANY}/drafts/D-1/accepted`,
    ]);
    expect(fake.posts[1]!.body).toEqual({
      voucher_id: first.voucher_id,
      voucher_no: first.voucher_no,
    });
    expect(fake.rows[0]!.status).toBe("accepted");
    expect(fake.rows[0]!.accepted_voucher_id).toBe(first.voucher_id);

    // A DIFFERENT MACHINE: no session memo, no localStorage, no day-book key.
    // Only the server can answer — and it does.
    _resetAcceptedDraftCache();
    _resetDurableAcceptRecords();
    _resetLedgerCache();
    const err = await acceptPurchaseDraft(client, COMPANY, body).then(
      () => null,
      (e: unknown) => e,
    );
    expect(err).toBeInstanceOf(DraftAlreadyAcceptedError);
    expect((err as DraftAlreadyAcceptedError).voucherID).toBe(first.voucher_id);
    expect((err as DraftAlreadyAcceptedError).message).toMatch(
      /insightd records it/,
    );
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("the accepted draft is no longer offered as READY, and names its voucher", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    _setDraftLifecyclePort(fake.port);

    const before = await fake.port.find(COMPANY, body);
    expect(before?.status).toBe("ready");

    const res = await acceptPurchaseDraft(client, COMPANY, body);
    await flush();

    // ⛔ THE ROOT-CAUSE ASSERTION. The row that used to read "…· ready · 95%"
    // after a successful accept now reads accepted, and carries the voucher, so the
    // inbox can say "already posted as Purchase N" instead of inviting a re-post.
    const after = await fake.port.find(COMPANY, body);
    expect(after?.status).toBe("accepted");
    expect(after?.status).not.toBe("ready");
    expect(after?.acceptedVoucherNo).toBe(res.voucher_no);
  });

  it("uses opts.draftId directly when the caller has it (no body-match scan)", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    const listed = vi.fn();
    _setDraftLifecyclePort(
      portWith({
        find: (companyID, b, draftId) => {
          listed(draftId);
          return fake.port.find(companyID, b, draftId);
        },
        claim: fake.port.claim,
        releaseClaim: fake.port.releaseClaim,
        recordAccepted: fake.port.recordAccepted,
      }),
    );

    await acceptPurchaseDraft(client, COMPANY, body, { draftId: "D-1" });
    await flush();
    expect(listed).toHaveBeenCalledWith("D-1");
    expect(fake.posts.map((p) => p.path)).toEqual([
      `/v1/companies/${COMPANY}/drafts/D-1/claim`,
      `/v1/companies/${COMPANY}/drafts/D-1/accepted`,
    ]);
  });

  it("picks the ACCEPTED draft when two identical bodies exist (same bill uploaded twice)", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body, body]);
    fake.rows[1]!.status = "accepted";
    fake.rows[1]!.accepted_voucher_id = "v-old";
    fake.rows[1]!.accepted_voucher_no = "3";
    _setDraftLifecyclePort(fake.port);

    const rec = await fake.port.find(COMPANY, body);
    // The accepted one wins: it is the answer that stops a duplicate. Returning the
    // still-ready D-1 would post the bill again and mark the wrong draft.
    expect(rec?.draftId).toBe("D-2");
    expect(rec?.acceptedVoucherNo).toBe("3");

    const spy = vi.spyOn(client, "postPurchaseVoucher");
    await expect(
      acceptPurchaseDraft(client, COMPANY, body),
    ).rejects.toBeInstanceOf(DraftAlreadyAcceptedError);
    expect(spy).not.toHaveBeenCalled();
  });

  it("has no opinion about a body insightd does not hold, and posts it", async () => {
    const client = createMockClient({ seed: false });
    const mine = await numberlessDraftBody(client);
    const someoneElses = await numberlessDraftBody(client);
    someoneElses.postings[0]!.amount = "77.0000";
    const fake = fakeInsightd([someoneElses]);
    _setDraftLifecyclePort(fake.port);

    // A first accept of a bill insightd has never seen must not be refused — an
    // exact body match, not a resemblance to "a bill from this supplier".
    expect(await fake.port.find(COMPANY, mine)).toBeNull();
    await expect(
      acceptPurchaseDraft(client, COMPANY, mine),
    ).resolves.toBeTruthy();
    await flush();
    expect(fake.posts).toHaveLength(0); // nothing to mark
  });

  it("an insightd OUTAGE degrades to the other layers instead of blocking the accept", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    _setDraftLifecyclePort(
      portWith({
        find: () => Promise.reject(new Error("ECONNREFUSED 127.0.0.1:8088")),
        // ⛔ THE CLAIM FAILS TOO — an outage takes the whole service down, not one
        // route. A claim that could not be TAKEN must degrade, not refuse: see
        // claimDraftForPost. This is the residual window, asserted rather than implied.
        claim: () => Promise.reject(new Error("ECONNREFUSED 127.0.0.1:8088")),
        releaseClaim: () =>
          Promise.reject(new Error("ECONNREFUSED 127.0.0.1:8088")),
        recordAccepted: () =>
          Promise.reject(new Error("ECONNREFUSED 127.0.0.1:8088")),
      }),
    );

    // ⛔ The FIRST accept of a non-duplicate bill must still post. Refusing here
    // would mean an insightd hiccup takes away the human's only write path, which
    // is a worse failure than losing the fourth of four duplicate checks.
    await expect(
      acceptPurchaseDraft(client, COMPANY, body),
    ).resolves.toBeTruthy();
    await flush();
    expect(warn).toHaveBeenCalled(); // reported, never silent

    // …and layer 3 still refuses the reload-then-accept-again case meanwhile.
    _resetAcceptedDraftCache();
    await expect(
      acceptPurchaseDraft(client, COMPANY, body),
    ).rejects.toBeInstanceOf(DraftAlreadyAcceptedError);
  });

  it("a 409 from insightd is NOT an error — it means the record was already there", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    fake.rows[0]!.status = "accepted"; // recorded by another tab a moment ago
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    _setDraftLifecyclePort(
      portWith({
        // find() reports "ready" (a stale read), so the accept proceeds and the
        // CLAIM is the thing that discovers the race — earlier than the POST used to.
        find: () =>
          Promise.resolve({
            draftId: "D-1",
            status: "ready",
            acceptedVoucherID: "",
            acceptedVoucherNo: "",
          }),
        releaseClaim: fake.port.releaseClaim,
        recordAccepted: fake.port.recordAccepted,
      }),
    );

    await expect(
      acceptPurchaseDraft(client, COMPANY, body),
    ).resolves.toBeTruthy();
    await flush();
    expect(warn).not.toHaveBeenCalled();
  });

  it("marking insightd never turns a SUCCESSFUL post into a failure, even on a SYNCHRONOUS throw", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    _setDraftLifecyclePort(
      portWith({
        find: () =>
          Promise.resolve({
            draftId: "D-1",
            status: "ready",
            acceptedVoucherID: "",
            acceptedVoucherNo: "",
          }),
        // A port that THROWS rather than rejecting. Nothing in the interface forbids
        // it, and an unguarded `void p.recordAccepted(...)` would let this escape past
        // a POST that already moved money.
        recordAccepted: () => {
          throw new Error("boom, synchronously");
        },
      }),
    );

    const res = await acceptPurchaseDraft(client, COMPANY, body);
    expect(res.voucher_id).toBeTruthy();
    expect(warn).toHaveBeenCalled();
  });

  it("marking insightd never turns a SUCCESSFUL post into a failure", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    _setDraftLifecyclePort(
      portWith({
        find: () =>
          Promise.resolve({
            draftId: "D-1",
            status: "ready",
            acceptedVoucherID: "",
            acceptedVoucherNo: "",
          }),
        recordAccepted: () =>
          Promise.reject(new ApiError(500, "insightd exploded")),
      }),
    );

    // The payable IS posted. An operator told "Error" after the money moved retries
    // — and posts the bill twice, which is the defect arriving by the error path.
    const res = await acceptPurchaseDraft(client, COMPANY, body);
    expect(res.voucher_id).toBeTruthy();
    await flush();
    expect(warn).toHaveBeenCalled();
    expect(String(warn.mock.calls[0]![0])).toMatch(
      /may still offer it as ready/,
    );
  });
});

describe("LAYER 2 — the day book, asked about the PRISTINE draft and not only what is posted", () => {
  it("⛔ refuses an EDITED re-accept whose new DATE moved the bill out of the day-book window", async () => {
    // The third door, found by re-reading this path after the re-drive: the guard
    // keys on the pristine draft but postDraftBody used to ask the books about the
    // EDITED body, so changing the date after a reload bought a second post of the
    // same NUMBERED supplier bill.
    const client = createMockClient({ seed: false });
    const body = await realDraftBody(client, true); // carries INV-9001 + a date
    const spy = vi.spyOn(client, "postPurchaseVoucher");
    await acceptPurchaseDraft(client, COMPANY, body);

    // A RELOAD ON A FRESH MACHINE: layers 1, 3 and 4 all blank, so the ONLY thing
    // that can refuse this is the day book — and only if it is asked about the
    // pristine draft's own date.
    _resetAcceptedDraftCache();
    _resetDurableAcceptRecords();
    _resetLedgerCache();

    const err = await acceptEditedPurchaseDraft(client, COMPANY, body, {
      voucherDate: "2026-04-03", // the draft's own date is 2026-04-02
    }).then(
      () => null,
      (e: unknown) => e,
    );

    expect(err).toBeInstanceOf(DraftAlreadyAcceptedError);
    expect((err as DraftAlreadyAcceptedError).voucherNo).toBe("INV-9001");
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("still refuses when the EDIT is what collides with an existing bill", async () => {
    // The mirror case, and why BOTH the pristine and the edited identity are asked
    // about: here the pristine draft is not on the books but the edited date is.
    const client = createMockClient({ seed: false });
    const first = await realDraftBody(client, true);
    await acceptPurchaseDraft(client, COMPANY, first);

    _resetAcceptedDraftCache();
    _resetDurableAcceptRecords();
    _resetLedgerCache();

    // A second draft of the same numbered bill, mis-read by OCR onto another date.
    const second = await realDraftBody(client, true);
    second.voucher_date = "2026-04-05";
    const spy = vi.spyOn(client, "postPurchaseVoucher");
    await expect(
      acceptEditedPurchaseDraft(client, COMPANY, second, {
        voucherDate: "2026-04-02", // corrected back onto the day the bill is already on
      }),
    ).rejects.toBeInstanceOf(DraftAlreadyAcceptedError);
    expect(spy).not.toHaveBeenCalled();
  });
});

describe("THE CONTROL — a normal FIRST accept is untouched by all four layers", () => {
  it("posts a complete, correctly-oriented purchase carrying its bill allocations, and marks the draft", async () => {
    const client = createMockClient({ seed: false });
    const body = await realDraftBody(client, true);
    const fake = fakeInsightd([body]);
    _setDraftLifecyclePort(fake.port);
    const spy = vi.spyOn(client, "postPurchaseVoucher");

    const res = await acceptPurchaseDraft(client, COMPANY, body);
    await flush();

    expect(spy).toHaveBeenCalledTimes(1);
    const [, req] = spy.mock.calls[0]!;

    // ORIENTATION: the party is CREDITED, the expense and the INPUT taxes DEBITED.
    const byLedger = new Map((req.postings ?? []).map((p) => [p.ledger_id, p]));
    expect(byLedger.get(body.party_ledger_id!)!.dr_cr).toBe("C");
    expect(byLedger.get(body.party_ledger_id!)!.amount).toBe("1180.0000");
    expect(byLedger.get(body.postings[0]!.ledger_id!)!.dr_cr).toBe("D");
    expect(byLedger.get(body.postings[0]!.ledger_id!)!.amount).toBe(
      "1000.0000",
    );
    expect(byLedger.get(body.gst!.cgst_ledger_id!)!.dr_cr).toBe("D");
    expect(byLedger.get(body.gst!.sgst_ledger_id!)!.dr_cr).toBe("D");

    // THE BILL REFERENCE the panel certified, on the wire and in the books.
    expect(req.bill_allocations).toEqual([
      {
        ledger_id: body.party_ledger_id,
        ref_name: "",
        ref_type: "NEW",
        dr_cr: "C",
        amount: "1180.0000",
      },
    ]);
    const out = await client.getOutstandings(COMPANY, { kind: "PAYABLE" });
    expect(out.parties).toHaveLength(1);
    expect(out.parties[0]!.pending).toBe("1180.0000");
    expect(out.parties[0]!.bills.map((b) => b.ref_name)).toEqual(["INV-9001"]);

    // GST: the server's own split, echoed back (never computed here).
    expect(res.cgst).toBe("90.0000");
    expect(res.sgst).toBe("90.0000");
    expect(res.igst).toBe("0.0000");

    // …and the draft is terminal, exactly once, having been RESERVED before the
    // voucher was posted and CONFIRMED after — claim -> post -> confirm.
    expect(fake.rows[0]!.status).toBe("accepted");
    expect(fake.posts.map((p) => p.path)).toEqual([
      `/v1/companies/${COMPANY}/drafts/D-1/claim`,
      `/v1/companies/${COMPANY}/drafts/D-1/accepted`,
    ]);
    // The confirm CLEARED the lease: an accepted draft is nobody's work in progress.
    expect(fake.rows[0]!.claim_token).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// LAYER 5 — THE POSTING CLAIM. The layer that had a server half and NO CLIENT.
//
// ⛔ EVERY TEST BELOW WOULD HAVE PASSED VACUOUSLY BEFORE THE CLIENT WAS WIRED, had
// it only asserted the 409. So each one asserts THE VOUCHER COUNT: how many times
// `postPurchaseVoucher` was actually called. That is the number that was 2 when two
// browsers were raced on one bill, and it is the only number that says whether the
// books got the bill twice.
// ---------------------------------------------------------------------------
describe("LAYER 5 — the posting claim (reserve before posting, not check before posting)", () => {
  /** Two "browsers": two lifecycle ports over ONE fake insightd, two claim tokens. */
  function twoPosters(fake: ReturnType<typeof fakeInsightd>) {
    // The module holds ONE page token, so the second racer is simulated by a port
    // that rewrites the token — which is exactly what a second browser is on the
    // wire: the same routes, a different token.
    const other = "f9-the-OTHER-browser";
    return {
      other,
      asOtherBrowser: portWith({
        find: fake.port.find,
        claim: (c, d, _t, by) => fake.port.claim(c, d, other, by),
        releaseClaim: (c, d) => fake.port.releaseClaim(c, d, other),
        recordAccepted: fake.port.recordAccepted,
      }),
    };
  }

  it("⛔⛔ TWO CONCURRENT POSTERS: exactly ONE voucher, and the loser posts NOTHING", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    const { asOtherBrowser } = twoPosters(fake);
    const spy = vi.spyOn(client, "postPurchaseVoucher");
    const ledgers = vi.spyOn(client, "createLedger");

    // BROWSER A takes the claim and holds it (its post is still in flight).
    _setDraftLifecyclePort(fake.port);
    await fake.port.claim(COMPANY, "D-1", _pageClaimToken(), "browser A");

    // BROWSER B now tries the whole accept. Separate localStorage, separate session
    // memo, a numberless bill the day book cannot see, and insightd still says
    // "ready" — every one of layers 1-4 waves it through. Only the claim stops it.
    _resetAcceptedDraftCache();
    _resetDurableAcceptRecords();
    _setDraftLifecyclePort(asOtherBrowser);
    const err = await acceptPurchaseDraft(client, COMPANY, body).then(
      () => null,
      (e: unknown) => e,
    );

    expect(err).toBeInstanceOf(DraftBeingPostedError);
    // ⛔ THE ASSERTION THAT MATTERS. Not "it threw" — the BOOKS.
    expect(spy).not.toHaveBeenCalled();
    // ⛔ AND NOT A SINGLE LEDGER MASTER EITHER: the claim precedes
    // resolveDraftLedgers, and cored has no route that deletes a ledger.
    expect(ledgers).not.toHaveBeenCalled();
    // The operator is told it is being posted — NOT that it is already posted.
    expect((err as Error).message).toMatch(/already being posted/i);
    expect((err as Error).message).toMatch(/NOTHING has been posted/i);
    expect(err).not.toBeInstanceOf(DraftAlreadyAcceptedError);
  });

  it("⛔ the WINNER still posts exactly one complete voucher while the loser is refused", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    const { asOtherBrowser } = twoPosters(fake);
    const spy = vi.spyOn(client, "postPurchaseVoucher");

    // Both browsers race the FULL accept path, concurrently.
    _setDraftLifecyclePort(fake.port);
    const a = acceptPurchaseDraft(client, COMPANY, body).then(
      (r) => ({ ok: true as const, r }),
      (e: unknown) => ({ ok: false as const, e }),
    );
    const first = await a;

    _resetAcceptedDraftCache();
    _resetDurableAcceptRecords();
    _setDraftLifecyclePort(asOtherBrowser);
    const second = await acceptPurchaseDraft(client, COMPANY, body).then(
      (r) => ({ ok: true as const, r }),
      (e: unknown) => ({ ok: false as const, e }),
    );

    // EXACTLY ONE of them wrote, and the books hold ONE purchase for this bill.
    expect([first.ok, second.ok].filter(Boolean)).toHaveLength(1);
    expect(spy).toHaveBeenCalledTimes(1);
    const day = await client.getDayBook(COMPANY, {
      from: body.voucher_date!,
      to: body.voucher_date!,
    });
    expect(
      day.entries.filter((e) => e.class.toLowerCase() === "purchase"),
    ).toHaveLength(1);
  });

  it("⛔⛔ CLAIM BEFORE THE FIRST WRITE OF ANY KIND — ledger masters INCLUDED", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    // ⛔ A PARTY THAT DOES NOT EXIST YET, so resolveDraftLedgers really does create a
    // ledger MASTER. Without this the ordering assertion below is vacuous: it would
    // pass just as happily with the claim moved to immediately before the voucher
    // POST, which is the very ordering that leaves a losing racer's orphan masters
    // behind. cored has no route that deletes a ledger, so they cannot be undone.
    body.party = "Brand New Supplier Pvt Ltd";
    body.party_ledger_id = null;
    body.postings[body.postings.length - 1]!.ledger_id = null;
    body.postings[body.postings.length - 1]!.ledger_name = body.party;
    const fake = fakeInsightd([body]);
    _setDraftLifecyclePort(fake.port);

    // ONE ordered log of both services. A claim recorded after ANY of these is the
    // check-then-act defect wearing a claim's clothes: the loser would already have
    // written something it cannot take back.
    const order: string[] = [];
    const realCreate = client.createLedger.bind(client);
    vi.spyOn(client, "createLedger").mockImplementation(async (c, req) => {
      order.push("ledger-master");
      return realCreate(c, req);
    });
    const realPost = client.postPurchaseVoucher.bind(client);
    vi.spyOn(client, "postPurchaseVoucher").mockImplementation(
      async (c, req) => {
        order.push("voucher-post");
        return realPost(c, req);
      },
    );
    const realClaim = fake.port.claim;
    _setDraftLifecyclePort(
      portWith({
        find: fake.port.find,
        claim: (c, d, t, by) => {
          order.push("claim");
          return realClaim(c, d, t, by);
        },
        releaseClaim: fake.port.releaseClaim,
        recordAccepted: (c, d, v) => {
          order.push("confirm");
          return fake.port.recordAccepted(c, d, v);
        },
      }),
    );

    await acceptPurchaseDraft(client, COMPANY, body);
    await flush();

    // The claim is FIRST. Not first-among-the-writes — first, period.
    expect(order).toEqual([
      "claim",
      "ledger-master",
      "voucher-post",
      "confirm",
    ]);
    expect(order.indexOf("claim")).toBeLessThan(order.indexOf("ledger-master"));
    expect(order.indexOf("claim")).toBeLessThan(order.indexOf("voucher-post"));
  });

  it("⛔ THE CONTROL: a FAILED post RELEASES the claim, so the bill stays POSTABLE", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    _setDraftLifecyclePort(fake.port);
    const spy = vi
      .spyOn(client, "postPurchaseVoucher")
      .mockRejectedValueOnce(new Error("422: back-dated beyond the cutoff"));

    await expect(acceptPurchaseDraft(client, COMPANY, body)).rejects.toThrow(
      /422/,
    );

    // ⛔ THE ANTI-STRANDING ASSERTION. A draft left claimed after a failed post is a
    // bill the operator can neither post NOR release — worse than the duplicate this
    // whole layer prevents. The release is on the wire, and the lease is GONE.
    expect(fake.posts.map((p) => p.path)).toEqual([
      `/v1/companies/${COMPANY}/drafts/D-1/claim`,
      `/v1/companies/${COMPANY}/drafts/D-1/claim/release`,
    ]);
    expect(fake.rows[0]!.claim_token).toBeNull();
    expect(fake.rows[0]!.status).toBe("ready");

    // …and the operator who fixes the problem can post it, WITHOUT waiting for a TTL.
    _resetAcceptedDraftCache();
    _resetDurableAcceptRecords();
    await expect(
      acceptPurchaseDraft(client, COMPANY, body),
    ).resolves.toBeTruthy();
    expect(spy).toHaveBeenCalledTimes(2);
    expect(fake.rows[0]!.status).toBe("accepted");
  });

  it("releases the claim when the failure is BEFORE the post (the chart read 500s)", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    _setDraftLifecyclePort(fake.port);
    const spy = vi.spyOn(client, "postPurchaseVoucher");
    // ⛔ A FAILURE STRICTLY BETWEEN THE CLAIM AND THE POST. If the release lived in
    // the post's own catch instead of around the whole region, this claim would be
    // stranded for the full TTL by a read that wrote nothing at all.
    vi.spyOn(client, "listLedgers").mockRejectedValue(
      new ApiError(500, "the chart of accounts could not be read"),
    );

    await expect(acceptPurchaseDraft(client, COMPANY, body)).rejects.toThrow(
      /chart of accounts/,
    );

    // No voucher was posted, so the reservation is owed back — the try that releases
    // starts at the CHART READ, not at the post, because everything between them can
    // fail too. Stranding the bill here would be a self-inflicted outage.
    expect(spy).not.toHaveBeenCalled();
    expect(fake.posts.map((p) => p.path)).toContain(
      `/v1/companies/${COMPANY}/drafts/D-1/claim/release`,
    );
    expect(fake.rows[0]!.claim_token).toBeNull();
  });

  it("a release that FAILS never replaces the error the operator has to read", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    _setDraftLifecyclePort(
      portWith({
        find: fake.port.find,
        claim: fake.port.claim,
        releaseClaim: () => Promise.reject(new Error("ECONNREFUSED")),
      }),
    );
    vi.spyOn(client, "postPurchaseVoucher").mockRejectedValueOnce(
      new Error("422: back-dated beyond the cutoff"),
    );

    // The 422 is what surfaces — not "ECONNREFUSED". The claim then lapses via the
    // TTL, which is exactly what the TTL is the backstop for.
    await expect(acceptPurchaseDraft(client, COMPANY, body)).rejects.toThrow(
      /422/,
    );
    expect(warn).toHaveBeenCalled();
    expect(String(warn.mock.calls[0]![0])).toMatch(/lapses on its own/);
  });

  it("the SAME page may re-claim its own draft — a retry is never locked out by itself", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    _setDraftLifecyclePort(fake.port);
    // This page already holds a live claim (a previous attempt that never released).
    await fake.port.claim(COMPANY, "D-1", _pageClaimToken(), "this page");

    // Idempotent for the same token: the lease renews rather than refusing.
    await expect(
      acceptPurchaseDraft(client, COMPANY, body),
    ).resolves.toBeTruthy();
  });

  it("a LAPSED claim does not strand the bill (the crashed-browser backstop)", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    const { asOtherBrowser } = twoPosters(fake);

    // A browser claimed the draft and DIED — it never posted and never released.
    _setDraftLifecyclePort(fake.port);
    await fake.port.claim(
      COMPANY,
      "D-1",
      _pageClaimToken(),
      "the dead browser",
    );
    fake.lapseClaims();

    // A permanent claim here would be a bill NOBODY can ever post, which is worse
    // than the duplicate. The lease has lapsed, so the next poster takes it over.
    _setDraftLifecyclePort(asOtherBrowser);
    await expect(
      acceptPurchaseDraft(client, COMPANY, body),
    ).resolves.toBeTruthy();
    expect(fake.rows[0]!.status).toBe("accepted");
  });

  it("a claim refused as ALREADY ACCEPTED is terminal, and names the voucher", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    const spy = vi.spyOn(client, "postPurchaseVoucher");
    // The other browser finished between our find() and our claim() — a window only
    // the claim itself can see, because find() had already returned "ready".
    _setDraftLifecyclePort(
      portWith({
        find: () =>
          Promise.resolve({
            draftId: "D-1",
            status: "ready",
            acceptedVoucherID: "",
            acceptedVoucherNo: "",
          }),
        claim: fake.port.claim,
        releaseClaim: fake.port.releaseClaim,
      }),
    );
    fake.rows[0]!.status = "accepted";
    fake.rows[0]!.accepted_voucher_id = "v-11";
    fake.rows[0]!.accepted_voucher_no = "11";

    const err = await acceptPurchaseDraft(client, COMPANY, body).then(
      () => null,
      (e: unknown) => e,
    );
    expect(err).toBeInstanceOf(DraftAlreadyAcceptedError);
    expect((err as DraftAlreadyAcceptedError).voucherNo).toBe("11");
    expect(spy).not.toHaveBeenCalled();
  });

  it("an UNRECOGNISED 409 still refuses — an unknown conflict is not permission to post", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const spy = vi.spyOn(client, "postPurchaseVoucher");
    _setDraftLifecyclePort(
      portWith({
        find: () =>
          Promise.resolve({
            draftId: "D-1",
            status: "ready",
            acceptedVoucherID: "",
            acceptedVoucherNo: "",
          }),
        claim: () =>
          Promise.reject(
            new ApiError(409, "some future conflict", { detail: {} }),
          ),
      }),
    );

    await expect(
      acceptPurchaseDraft(client, COMPANY, body),
    ).rejects.toBeInstanceOf(DraftBeingPostedError);
    expect(spy).not.toHaveBeenCalled();
  });

  it("⚠️ THE RESIDUAL, STATED: with NO draft id resolvable there is nothing to reserve", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const claim = vi.fn(() => Promise.resolve());
    // insightd holds no matching draft, so layer 4's find() returns null and there is
    // no address to claim. The accept must still go through (a first accept of a bill
    // insightd never saw is not a duplicate) — but the race is NOT covered here.
    // Passing opts.draftId removes this case entirely.
    _setDraftLifecyclePort(
      portWith({ find: () => Promise.resolve(null), claim }),
    );

    await expect(
      acceptPurchaseDraft(client, COMPANY, body),
    ).resolves.toBeTruthy();
    expect(claim).not.toHaveBeenCalled();
  });
});

describe("LAYER 5 on the EDITED accept path (the same funnel, the same reservation)", () => {
  it("⛔ an EDITED accept claims too, and claims on the PRISTINE draft's identity", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    _setDraftLifecyclePort(fake.port);

    // The operator edited the DATE. An earlier round's third door was exactly this:
    // the guard asked about the EDITED body, so editing the date bought a second post
    // of the same bill. The claim must resolve the draft by the PRISTINE body — which
    // it does, because it uses the id `identity` resolved, not the edited one.
    await acceptEditedPurchaseDraft(client, COMPANY, body, {
      voucherDate: "2026-07-13",
    });
    await flush();

    expect(fake.posts.map((p) => p.path)).toEqual([
      `/v1/companies/${COMPANY}/drafts/D-1/claim`,
      `/v1/companies/${COMPANY}/drafts/D-1/accepted`,
    ]);
    expect(fake.rows[0]!.status).toBe("accepted");
  });

  it("an EDITED accept whose claim is LOST posts nothing and creates no masters", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    const spy = vi.spyOn(client, "postPurchaseVoucher");
    const ledgers = vi.spyOn(client, "createLedger");
    _setDraftLifecyclePort(fake.port);
    // Another browser holds the claim.
    await fake.port.claim(COMPANY, "D-1", "f9-the-OTHER-browser", "browser A");

    await expect(
      acceptEditedPurchaseDraft(client, COMPANY, body, {
        voucherDate: "2026-07-13",
      }),
    ).rejects.toBeInstanceOf(DraftBeingPostedError);
    expect(spy).not.toHaveBeenCalled();
    expect(ledgers).not.toHaveBeenCalled();
  });
});

describe("D85 — the duplicate-bill guard, and the ADVICE it gives", () => {
  /**
   * ⛔ WHAT IS AND IS NOT PROVED HERE. The DISCRIMINATION lives in insightd
   * (`redaction/persistence.py` now appends a fingerprint of the removed value to
   * the tag it writes, so two different PANs no longer become one voucher number);
   * `insightd/tests/test_pii_persistence.py` is where that is proved and it is RED
   * without the fix. This file's share of D85 is the half the client owns: the
   * SENTENCE. The old one advised "key its own supplier invoice number" for a
   * number that had been redacted, which was a dead end — a refusal fully present
   * and fully wrong. Only the first two cases below are red on the previous commit.
   */
  const TAG_A = "[PAN REDACTED 6442]"; // sha256("ABCDE1234F")[:4]
  const TAG_B = "[PAN REDACTED 0413]"; // sha256("ZZZZZ9999Z")[:4]

  const refusalFor = async (
    client: CoredClient,
    body: VoucherDraftBody,
    opts?: { draftId?: string },
  ): Promise<DraftAlreadyAcceptedError> => {
    const err = await acceptPurchaseDraft(client, COMPANY, body, opts).then(
      () => null,
      (e: unknown) => e,
    );
    expect(err).toBeInstanceOf(DraftAlreadyAcceptedError);
    return err as DraftAlreadyAcceptedError;
  };

  it("tells a REDACTED number's refusal apart from an ordinary one — the fingerprint sentence", async () => {
    const client = createMockClient({ seed: false });
    const first = balancedBody();
    first.voucher_no = TAG_A;
    await acceptPurchaseDraft(client, COMPANY, first);

    // A RELOAD, so only the day-book layer can speak — the same shape as the
    // "still refuses AFTER A PAGE RELOAD" case above.
    _resetAcceptedDraftCache();
    _resetDurableAcceptRecords();
    const again = balancedBody();
    again.voucher_no = TAG_A;

    const err = await refusalFor(client, again);
    expect(err.message).toMatch(/fingerprint of the removed number/);
    expect(err.message).toMatch(/SAME supplier number/);
    // ⛔ AND IT MUST NOT STILL GIVE THE DEAD-END ADVICE. This is the assertion the
    // previous commit fails: the sentence was unconditional.
    expect(err.message).not.toMatch(/key its own supplier invoice number/);
  });

  it("keeps the ORDINARY advice for an ordinary number — the fix is not a blanket rewrite", async () => {
    // ⚠️ Green before the change, and it is here to refuse the WRONG fix: replacing
    // the sentence unconditionally would tell an operator holding a plain "INV-9001"
    // about a fingerprint that is not in their number.
    const client = createMockClient({ seed: false });
    await acceptPurchaseDraft(client, COMPANY, balancedBody()); // voucher_no INV-9001
    _resetAcceptedDraftCache();
    _resetDurableAcceptRecords();

    const err = await refusalFor(client, balancedBody());
    expect(err.voucherNo).toBe("INV-9001");
    expect(err.message).toMatch(/key its own supplier invoice number/);
    expect(err.message).not.toMatch(/fingerprint/);
  });

  it("names the KEY it actually refused on: the draft when one was passed, the content otherwise", async () => {
    // ⛔ THE OTHER HALF OF "THE ADVICE MUST MATCH THE KEY". The session and durable
    // memos key on `draftAcceptKey`, which is the DRAFT id when the caller passes
    // one and the body's CONTENT when it does not. Those have two different
    // remedies, and the old sentence gave the content one in both cases — which is
    // the wrong remedy exactly when the caller is most precise. ⚠️ Note which branch
    // the product takes today: the F9 screen passes no draftId.
    const client = createMockClient({ seed: false });
    const body = balancedBody();
    await acceptPurchaseDraft(client, COMPANY, body, { draftId: "d-77" });
    const keyed = await refusalFor(client, body, { draftId: "d-77" });
    expect(keyed.message).toMatch(/keyed on the DRAFT/);
    expect(keyed.message).toMatch(/upload it as its own document/);
    expect(keyed.message).not.toMatch(/key its own supplier invoice number/);

    // ⚠️ A FRESH CLIENT, AND DELIBERATELY NO CACHE RESET. Resetting the memos is
    // what lets the DAY BOOK speak (the case above), and the day book would then
    // answer this one too — a vacuous pass on the wrong layer's sentence. An empty
    // book plus a live memo leaves exactly one layer able to refuse.
    const client2 = createMockClient({ seed: false });
    const other = balancedBody();
    await acceptPurchaseDraft(client2, COMPANY, other);
    const content = await refusalFor(client2, other);
    expect(content.message).toMatch(/keyed on the draft's own CONTENT/);
    expect(content.message).toMatch(/key its own supplier invoice number/);
  });

  it("posts BOTH bills when the two tags differ — the client adds no opinion of its own", async () => {
    // ⚠️ Green before the change BY CONSTRUCTION, and that is the point worth
    // pinning: the client compares strings and nothing else, so the whole of D85's
    // discrimination is upstream in insightd. If a later session "helpfully" makes
    // this guard normalise or truncate a redaction tag, the over-refusal comes
    // straight back and this is what notices.
    const client = createMockClient({ seed: false });
    const spy = vi.spyOn(client, "postPurchaseVoucher");
    const a = balancedBody();
    a.voucher_no = TAG_A;
    const b = balancedBody();
    b.voucher_no = TAG_B;
    b.postings[0]!.amount = "2000.0000"; // a different bill, different money
    b.postings[3]!.amount = "2180.0000";
    b.gst!.lines[0]!.taxable_value = "2000.0000";

    await acceptPurchaseDraft(client, COMPANY, a);
    await acceptPurchaseDraft(client, COMPANY, b);
    expect(spy).toHaveBeenCalledTimes(2);
    // SIDES AND NAMES, not totals: each bill left its own party CREDIT leg.
    const credits = spy.mock.calls.flatMap((c) =>
      (c[1].postings ?? []).filter((p) => p.dr_cr === "C"),
    );
    expect(credits.map((p) => p.amount)).toEqual(["1180.0000", "2180.0000"]);
    expect(credits.every((p) => p.ledger_id === "ledger-acme")).toBe(true);
  });
});

/**
 * ═══════════════════════════════════════════════════════════════════════════
 * §7.3 B — THE SEVENTH DOOR: A CONFIRM THAT NEVER LANDED, AND NOTHING WENT BACK
 *          FOR IT.
 * ═══════════════════════════════════════════════════════════════════════════
 * The claim -> post -> confirm reorder closes the race it was built for. The
 * CONFIRM did not have the same care: `recordAccepted` was fire-and-forget with a
 * `console.warn`, so a confirm aborted at the network layer left insightd's draft
 * `ready` — LAYER 4, the only layer not confined to one browser profile, with no
 * record of an acceptance that had already moved money — while the operator was
 * told "Saved". The claim's TTL then became the duplicate's timer.
 *
 * ⚠️ READ THE LIMITATION TEST AT THE END BEFORE CALLING THIS CLOSED. The queue is
 * localStorage, so only the browser that posted can drive the retry.
 */
describe("§7.3 B — the pending-confirm queue", () => {
  /** A port whose confirm fails the way an aborted request does. */
  const confirmFails = (base: DraftLifecyclePort, err: unknown) =>
    portWith({
      find: base.find.bind(base),
      claim: base.claim.bind(base),
      releaseClaim: base.releaseClaim.bind(base),
      recordAccepted: () => Promise.reject(err),
    });

  it("OWES the confirm when it fails, naming the voucher that already exists", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    _setDraftLifecyclePort(
      confirmFails(fake.port, new TypeError("Failed to fetch")),
    );
    vi.spyOn(console, "warn").mockImplementation(() => {});

    const res = await acceptPurchaseDraft(client, COMPANY, body);
    await flush();

    // The accept SUCCEEDED — bookkeeping about a posted voucher may never turn a
    // success into a failure, and that rule is unchanged.
    expect(res.voucher_id).toBeTruthy();
    // insightd is in the defect state: the draft is still offered as ready.
    expect(fake.rows[0]!.status).toBe("ready");
    // ⛔ THE FIX: the confirm is written down as OWED, against the voucher that
    // exists, instead of surviving only as a console line.
    const owed = _pendingDraftConfirms();
    expect(owed).toHaveLength(1);
    expect(owed[0]!.draftId).toBe("D-1");
    expect(owed[0]!.voucherID).toBe(res.voucher_id);
    expect(owed[0]!.voucherNo).toBe(res.voucher_no);
    expect(owed[0]!.companyID).toBe(COMPANY);
  });

  it("owes it even when the confirm NEVER SETTLES — the aborted-request case", async () => {
    // The reported failure: browser A posted purchase 6, the confirm was aborted
    // at the network layer, and the operator was told "Saved". A note written on
    // the RESPONSE would not exist here, which is why the queue is written first.
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    _setDraftLifecyclePort(
      portWith({
        find: fake.port.find.bind(fake.port),
        claim: fake.port.claim.bind(fake.port),
        recordAccepted: () => new Promise<void>(() => {}), // never settles
      }),
    );

    await acceptPurchaseDraft(client, COMPANY, body);
    await flush();

    expect(pendingDraftConfirmCount(COMPANY)).toBe(1);
  });

  it("a 409 is an ACKNOWLEDGEMENT, so nothing stays owed", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    _setDraftLifecyclePort(
      confirmFails(fake.port, new ApiError(409, "already accepted")),
    );
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});

    await acceptPurchaseDraft(client, COMPANY, body);
    await flush();

    // insightd already holds the record, which is the state the queue exists to
    // reach — so it is not owed, and it is not a warning either.
    expect(_pendingDraftConfirms()).toEqual([]);
    expect(warn).not.toHaveBeenCalled();
  });

  it("a SUCCESSFUL confirm leaves nothing owed (the control)", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    _setDraftLifecyclePort(fake.port);

    await acceptPurchaseDraft(client, COMPANY, body);
    await flush();

    expect(fake.rows[0]!.status).toBe("accepted");
    expect(_pendingDraftConfirms()).toEqual([]);
  });

  it("the drain lands the confirm once insightd is reachable again", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    _setDraftLifecyclePort(
      confirmFails(fake.port, new TypeError("Failed to fetch")),
    );
    vi.spyOn(console, "warn").mockImplementation(() => {});

    const res = await acceptPurchaseDraft(client, COMPANY, body);
    await flush();
    expect(pendingDraftConfirmCount()).toBe(1);

    // The network comes back.
    _setDraftLifecyclePort(fake.port);
    const stillOwed = await drainPendingDraftConfirms(COMPANY);

    expect(stillOwed).toBe(0);
    expect(_pendingDraftConfirms()).toEqual([]);
    // ⛔ THE POINT: layer 4 is restored. The draft is terminal and names the
    // voucher, so the inbox stops offering it and a DIFFERENT machine is covered.
    expect(fake.rows[0]!.status).toBe("accepted");
    expect(fake.rows[0]!.accepted_voucher_id).toBe(res.voucher_id);
    expect(fake.rows[0]!.accepted_voucher_no).toBe(res.voucher_no);
  });

  it("a still-broken insightd keeps the confirm owed and COUNTS the attempts", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    _setDraftLifecyclePort(
      confirmFails(fake.port, new TypeError("Failed to fetch")),
    );
    vi.spyOn(console, "warn").mockImplementation(() => {});

    await acceptPurchaseDraft(client, COMPANY, body);
    await flush();
    const afterPost = _pendingDraftConfirms()[0]!.attempts;

    // ⛔ AND IT MUST NOT THROW. The drain runs on the accept path and on a screen
    // mount; a rejection here would become a failed post or an error screen.
    const stillOwed = await drainPendingDraftConfirms(COMPANY);

    expect(stillOwed).toBe(1);
    expect(_pendingDraftConfirms()[0]!.attempts).toBeGreaterThan(afterPost);
    expect(fake.rows[0]!.status).toBe("ready");
  });

  it("⛔ THE DOOR: the NEXT accept from this browser settles the debt, and layer 4 then refuses the duplicate", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    _setDraftLifecyclePort(
      confirmFails(fake.port, new TypeError("Failed to fetch")),
    );
    const spy = vi.spyOn(client, "postPurchaseVoucher");
    vi.spyOn(console, "warn").mockImplementation(() => {});

    const first = await acceptPurchaseDraft(client, COMPANY, body);
    await flush();
    expect(fake.rows[0]!.status).toBe("ready"); // the lost confirm
    expect(pendingDraftConfirmCount(COMPANY)).toBe(1);

    // insightd comes back, and the claim's lease lapses — which is exactly the
    // moment the bill used to become postable again by anyone.
    _setDraftLifecyclePort(fake.port);
    fake.lapseClaims();
    // A PAGE RELOAD and a WIPED MEMO, so layers 1 and 3 have no opinion: the only
    // thing that can refuse this is the server-side record the drain restores.
    _resetAcceptedDraftCache();
    _resetDurableAcceptRecords();
    _resetLedgerCache();

    const err = await acceptPurchaseDraft(client, COMPANY, body).then(
      () => null,
      (e: unknown) => e,
    );

    expect(err).toBeInstanceOf(DraftAlreadyAcceptedError);
    expect((err as DraftAlreadyAcceptedError).voucherID).toBe(first.voucher_id);
    // ONE voucher for one supplier bill. That is the whole register row.
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("the drain is AWAITED on the accept path, not raced", async () => {
    // A drain kicked off un-awaited would leave layer 4's read racing it, which
    // makes the guard probabilistic — the quality that made the FIRST duplicate
    // fix insufficient. So the confirm must be settled before `find` is consulted.
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    _setDraftLifecyclePort(
      confirmFails(fake.port, new TypeError("Failed to fetch")),
    );
    vi.spyOn(console, "warn").mockImplementation(() => {});
    await acceptPurchaseDraft(client, COMPANY, body);
    await flush();

    const order: string[] = [];
    let confirmDone = false;
    _setDraftLifecyclePort(
      portWith({
        find: async (co, b, id) => {
          order.push(
            confirmDone ? "find-after-confirm" : "find-before-confirm",
          );
          return fake.port.find(co, b, id);
        },
        claim: fake.port.claim.bind(fake.port),
        releaseClaim: fake.port.releaseClaim.bind(fake.port),
        recordAccepted: async (co, id, v) => {
          // A real network hop, so a racing read would win if it were racing.
          await new Promise((r) => setTimeout(r, 5));
          await fake.port.recordAccepted(co, id, v);
          confirmDone = true;
        },
      }),
    );
    _resetAcceptedDraftCache();
    _resetDurableAcceptRecords();
    _resetLedgerCache();

    await acceptPurchaseDraft(client, COMPANY, body).catch(() => null);

    expect(order).toContain("find-after-confirm");
    expect(order).not.toContain("find-before-confirm");
  });

  it("an empty queue costs the accept path NO port calls at all", async () => {
    // The normal case is an empty queue, so the guard must be one storage read
    // and not a network round trip on every accept.
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    const recordAccepted = vi.fn(fake.port.recordAccepted.bind(fake.port));
    _setDraftLifecyclePort(
      portWith({
        find: fake.port.find.bind(fake.port),
        claim: fake.port.claim.bind(fake.port),
        releaseClaim: fake.port.releaseClaim.bind(fake.port),
        recordAccepted,
      }),
    );

    expect(pendingDraftConfirmCount(COMPANY)).toBe(0);
    await acceptPurchaseDraft(client, COMPANY, body);
    await flush();

    // Exactly one — the confirm for THIS accept, not a retry pass on top of it.
    expect(recordAccepted).toHaveBeenCalledTimes(1);
  });

  it("scopes the drain to the company asked for", async () => {
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    _setDraftLifecyclePort(
      confirmFails(fake.port, new TypeError("Failed to fetch")),
    );
    vi.spyOn(console, "warn").mockImplementation(() => {});
    await acceptPurchaseDraft(client, COMPANY, body);
    await flush();

    _setDraftLifecyclePort(fake.port);
    // Another company's drain must not touch this one's debt: an accept in
    // company B may not silently confirm a draft in company A.
    expect(await drainPendingDraftConfirms("co-OTHER")).toBe(0);
    expect(pendingDraftConfirmCount(COMPANY)).toBe(1);

    expect(await drainPendingDraftConfirms(COMPANY)).toBe(0);
    expect(pendingDraftConfirmCount(COMPANY)).toBe(0);
  });

  it("⛔ THE DOOR IS SHUT: a DIFFERENT browser with a lost confirm gets ONE voucher", async () => {
    // ⚠️ THIS TEST REPLACES A PIN, AND THE PIN IS WHY IT IS WORDED THIS WAY. Until
    // §7.3 B's server half landed, this exact scenario asserted TWO vouchers and
    // said so in a comment telling the next session to delete it when it went red.
    // It went red. What follows is its inverse, and it asserts the SAME scenario —
    // nothing about the setup is softened.
    const client = createMockClient({ seed: false });
    const body = await numberlessDraftBody(client);
    const fake = fakeInsightd([body]);
    _setDraftLifecyclePort(
      confirmFails(fake.port, new TypeError("Failed to fetch")),
    );
    const spy = vi.spyOn(client, "postPurchaseVoucher");
    vi.spyOn(console, "warn").mockImplementation(() => {});

    const first = await acceptPurchaseDraft(client, COMPANY, body);
    await flush();
    // The confirm really was lost: insightd still calls this bill `ready`.
    expect(pendingDraftConfirmCount(COMPANY)).toBe(1);
    expect(fake.rows[0]!.status).not.toBe("accepted");

    // A DIFFERENT MACHINE: no session memo, no durable memo, NO QUEUE, and a
    // lapsed claim. Every layer that lives in a browser is gone, and the one that
    // does not — the day book — has nothing to match on, because this draft's
    // supplier invoice number was never read by the OCR.
    _resetAcceptedDraftCache();
    _resetDurableAcceptRecords();
    _resetPendingDraftConfirms();
    _resetLedgerCache();
    fake.lapseClaims();
    _setDraftLifecyclePort(fake.port); // insightd reachable from here, but `ready`

    const err = await acceptPurchaseDraft(client, COMPANY, body).then(
      () => null,
      (e: unknown) => e,
    );

    // ⛔ THE REFUSAL IS TERMINAL AND IT NAMES THE VOUCHER. An operator told only
    // "refused" cannot tell a blocked duplicate from a bill that went missing.
    expect(err).toBeInstanceOf(DraftAlreadyAcceptedError);
    expect((err as DraftAlreadyAcceptedError).voucherNo).toBe(first.voucher_no);
    expect((err as DraftAlreadyAcceptedError).message).toContain(
      "NOTHING was posted",
    );

    // ⛔⛔ THE ASSERTION THAT IS THE FEATURE. Note that the second browser DID
    // attempt the POST — the guard is not "the client declined to try", which is
    // what all four client-side layers amount to and what a second machine walks
    // straight through. It is that the SERVER refused inside the voucher's own
    // transaction, so the attempt left no voucher behind.
    expect(spy).toHaveBeenCalledTimes(2);
    const day = await client.getDayBook(COMPANY, {
      from: "1970-01-01",
      to: "2999-12-31",
    });
    expect(
      day.entries.filter((e) => e.class.toLowerCase() === "purchase"),
    ).toHaveLength(1);

    // ⛔ AND THE BILL IS NOT WEDGED. The refusal taught insightd what the first
    // browser never managed to, so the inbox stops offering a bill that can no
    // longer be posted — otherwise closing the duplicate door would just move the
    // defect to "this row never goes away".
    await flush();
    expect(fake.rows[0]!.status).toBe("accepted");
    expect(fake.rows[0]!.accepted_voucher_no).toBe(first.voucher_no);
    expect(pendingDraftConfirmCount(COMPANY)).toBe(0);
  });
});
