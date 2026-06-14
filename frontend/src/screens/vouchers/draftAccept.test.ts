import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import {
  createMockClient,
  type CoredClient,
  type VoucherDraftBody,
} from "../../api";
import {
  draftBodyToVoucherRequest,
  resolveDraftLedgers,
  acceptPurchaseDraft,
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
      { ledger_id: "ledger-purchases", ledger_name: "Purchases", dr_cr: "D", amount: "1000.0000" },
      { ledger_id: "ledger-cgst", ledger_name: "Input CGST", dr_cr: "D", amount: "90.0000" },
      { ledger_id: "ledger-sgst", ledger_name: "Input SGST", dr_cr: "D", amount: "90.0000" },
      { ledger_id: "ledger-acme", ledger_name: "Acme Traders", dr_cr: "C", amount: "1180.0000" },
    ],
    gst: {
      ruleset_version: "gst-2024-09",
      supplier_state_code: "29",
      place_of_supply_state_code: "29",
      reverse_charge: false,
      exempt: false,
      lines: [{ taxable_value: "1000.0000", gst_rate_pct: "18.0000", hsn: "1234" }],
      cgst_ledger_id: "ledger-cgst",
      sgst_ledger_id: "ledger-sgst",
      igst_ledger_id: "ledger-igst",
    },
  };
}

beforeEach(() => {
  _resetLedgerCache();
  _resetOutputTaxLedgerCache();
});
afterEach(() => {
  vi.restoreAllMocks();
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
      lines: [{ taxable_value: "1000.0000", gst_rate_pct: "18.0000", hsn: "1234" }],
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
    expect(groups).toContainEqual({ name: "Acme Traders", group: "Sundry Creditors" });
    expect(groups).toContainEqual({ name: "Purchases", group: "Purchase Accounts" });
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
