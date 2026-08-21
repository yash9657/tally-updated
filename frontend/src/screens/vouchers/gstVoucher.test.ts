import { describe, it, expect, beforeEach, vi } from "vitest";
import { createMockClient, ApiError } from "../../api";
import type { CoredClient } from "../../api";
import {
  buildGstVoucherRequest,
  ensureOutputTaxLedgers,
  ensureGstTaxLedgers,
  ensureLedger,
  emptyGstFormState,
  _resetOutputTaxLedgerCache,
  _resetLedgerCache,
  chargeLinesRefusal,
  keyedChargeLines,
  type OutputTaxLedgers,
} from "./gstVoucher";
import { acceptVoucher } from "./editActions";

beforeEach(() => {
  _resetOutputTaxLedgerCache();
  _resetLedgerCache();
});

const OUTPUT: OutputTaxLedgers = {
  cgstId: "led-cgst",
  sgstId: "led-sgst",
  igstId: "led-igst",
};

describe("buildGstVoucherRequest", () => {
  it("intra-state emits party Dr (gross) + income Cr + CGST/SGST Cr, no IGST line", () => {
    const req = buildGstVoucherRequest({
      voucherType: "sales",
      partyLedgerId: "party-1",
      incomeLedgerId: "income-1",
      taxableValue: "10000.0000",
      supplierStateCode: "29",
      gst: { ...emptyGstFormState(), placeOfSupply: "29", gstRatePct: "18", hsn: "8471" },
      outputTax: OUTPUT,
      voucherNo: "INV/0001",
      voucherDate: "2025-05-10",
      party: "Acme Corp",
    });

    // GSTRequest is well-formed.
    expect(req.gst?.supplier_state_code).toBe("29");
    expect(req.gst?.place_of_supply_state_code).toBe("29");
    expect(req.gst?.lines?.[0]?.taxable_value).toBe("10000.0000");
    expect(req.gst?.lines?.[0]?.gst_rate_pct).toBe("18");
    expect(req.gst?.lines?.[0]?.hsn).toBe("8471");
    expect(req.gst?.cgst_ledger_id).toBe("led-cgst");
    expect(req.gst?.sgst_ledger_id).toBe("led-sgst");
    expect(req.gst?.igst_ledger_id).toBe("led-igst");

    // Balanced postings: party Dr 11800, income Cr 10000, CGST 900, SGST 900.
    const p = req.postings!;
    expect(p).toHaveLength(4);
    expect(p[0]).toMatchObject({ ledger_id: "party-1", dr_cr: "DR", amount: "11800.0000" });
    expect(p[1]).toMatchObject({ ledger_id: "income-1", dr_cr: "CR", amount: "10000.0000" });
    expect(p[2]).toMatchObject({ ledger_id: "led-cgst", dr_cr: "CR", amount: "900.0000" });
    expect(p[3]).toMatchObject({ ledger_id: "led-sgst", dr_cr: "CR", amount: "900.0000" });
    // No IGST line.
    expect(p.some((x) => x.ledger_id === "led-igst")).toBe(false);
    expect(req.voucher_no).toBe("INV/0001");
  });

  it("inter-state emits a single IGST line, no CGST/SGST", () => {
    const req = buildGstVoucherRequest({
      voucherType: "sales",
      partyLedgerId: "party-1",
      incomeLedgerId: "income-1",
      taxableValue: "10000.0000",
      supplierStateCode: "29",
      gst: { ...emptyGstFormState(), placeOfSupply: "27", gstRatePct: "18" },
      outputTax: OUTPUT,
    });
    const p = req.postings!;
    expect(p).toHaveLength(3);
    expect(p[0]).toMatchObject({ ledger_id: "party-1", dr_cr: "DR", amount: "11800.0000" });
    expect(p[1]).toMatchObject({ ledger_id: "income-1", dr_cr: "CR", amount: "10000.0000" });
    expect(p[2]).toMatchObject({ ledger_id: "led-igst", dr_cr: "CR", amount: "1800.0000" });
    expect(p.some((x) => x.ledger_id === "led-cgst")).toBe(false);
    expect(p.some((x) => x.ledger_id === "led-sgst")).toBe(false);
  });

  // WS8a: the supplier (home) state is no longer a hardcoded constant, so it can
  // legitimately be unknown for one round trip. When it is, the field must be
  // OMITTED from the wire body rather than sent as "", so the server's
  // company.state_code default (internal/ledger/ledger.go, inside the post tx) is
  // the explicit, single source of truth for the supplier state.
  it("omits supplier_state_code entirely when the home state is unknown", () => {
    const req = buildGstVoucherRequest({
      voucherType: "sales",
      partyLedgerId: "party-1",
      incomeLedgerId: "income-1",
      taxableValue: "10000.0000",
      supplierStateCode: "",
      gst: { ...emptyGstFormState(), placeOfSupply: "27", gstRatePct: "18" },
      outputTax: OUTPUT,
    });
    // Not merely undefined — the KEY must be absent (JSON.stringify drops it).
    expect("supplier_state_code" in (req.gst as object)).toBe(false);
    expect(JSON.stringify(req.gst)).not.toContain("supplier_state_code");
    // The other codes are untouched.
    expect(req.gst?.place_of_supply_state_code).toBe("27");

    // DOCUMENTED CONSEQUENCE: with no home state the client-side mirror cannot
    // classify the sale, so it derives inter-state (IGST) postings. The server
    // will fill the supplier state from company.state_code and re-derive; if the
    // company is in fact registered in 27 the posted legs will NOT match and the
    // post is REJECTED (422) — a loud failure, never a silently wrong tax. This
    // is why CompanyContext hydrates homeStateCode from the company master
    // before a gated screen renders instead of guessing a default.
    const p = req.postings!;
    expect(p).toHaveLength(3);
    expect(p[2]).toMatchObject({ ledger_id: "led-igst", dr_cr: "CR", amount: "1800.0000" });
  });

  it("sends supplier_state_code verbatim when the home state IS known", () => {
    const req = buildGstVoucherRequest({
      voucherType: "sales",
      partyLedgerId: "party-1",
      incomeLedgerId: "income-1",
      taxableValue: "10000.0000",
      supplierStateCode: "27",
      gst: { ...emptyGstFormState(), placeOfSupply: "27", gstRatePct: "18" },
      outputTax: OUTPUT,
    });
    expect(req.gst?.supplier_state_code).toBe("27");
    // A Maharashtra company selling within Maharashtra is INTRA-state: the
    // hardcoded "29" this workstream removed would have produced IGST here.
    const p = req.postings!;
    expect(p).toHaveLength(4);
    expect(p[2]).toMatchObject({ ledger_id: "led-cgst", dr_cr: "CR", amount: "900.0000" });
    expect(p[3]).toMatchObject({ ledger_id: "led-sgst", dr_cr: "CR", amount: "900.0000" });
    expect(p.some((x) => x.ledger_id === "led-igst")).toBe(false);
  });
});

describe("acceptVoucher with a GST request body", () => {
  async function seed() {
    const client = createMockClient({ seed: false });
    const { company_id } = await client.createCompany({
      name: "Acme",
      fy_start_date: "2025-04-01",
      base_currency: "INR",
    });
    return { client, company_id };
  }

  it("create-mode Sales sends a well-formed gst block and the mock echoes the derived split", async () => {
    const { client, company_id } = await seed();
    const out = await ensureOutputTaxLedgers(client, company_id);
    const party = await ensureLedger(client, company_id, {
      name: "Acme Corp",
      groupName: "Sundry Debtors",
      stateCode: "29",
    });
    const income = await ensureLedger(client, company_id, {
      name: "Domestic Sales",
      groupName: "Sales Accounts",
    });
    const req = buildGstVoucherRequest({
      voucherType: "sales",
      partyLedgerId: party,
      incomeLedgerId: income,
      taxableValue: "10000.0000",
      supplierStateCode: "29",
      gst: { ...emptyGstFormState(), placeOfSupply: "29", gstRatePct: "18", hsn: "8471" },
      outputTax: out,
    });
    const res = await acceptVoucher(
      client,
      company_id,
      { type: "sales", mode: "create" },
      req,
    );
    // The displayed split comes from the response (server-derived), echoed by mock.
    expect(res.cgst).toBe("900.0000");
    expect(res.sgst).toBe("900.0000");
    expect(res.igst).toBe("0.0000");
    expect(res.version_no).toBe(1);
  });
});

describe("ensureOutputTaxLedgers memoization", () => {
  it("creates the 3 Duties & Taxes ledgers once, then reuses cached ids", async () => {
    const base = createMockClient({ seed: false });
    const createLedger = vi.fn(base.createLedger.bind(base));
    const client = { ...base, createLedger } as unknown as CoredClient;

    const first = await ensureOutputTaxLedgers(client, "co-1");
    expect(createLedger).toHaveBeenCalledTimes(3);
    // Assert they went to Duties & Taxes.
    const groups = createLedger.mock.calls.map((c) => (c[1] as { group_name: string }).group_name);
    expect(groups).toEqual([
      "Duties & Taxes",
      "Duties & Taxes",
      "Duties & Taxes",
    ]);

    const second = await ensureOutputTaxLedgers(client, "co-1");
    // No new createLedger calls — cached ids reused.
    expect(createLedger).toHaveBeenCalledTimes(3);
    expect(second).toEqual(first);
  });
});

// ---------------------------------------------------------------------------
// ⛔⛔ ensureLedger AGAINST A LEDGER THAT ALREADY EXISTS.
//
// THE BUG THIS PINS, which was live and user-facing. `ensureLedger` called
// `createLedger` unconditionally, on the stated assumption — written in its own doc
// comment — that the route was idempotent by name. It is not: `INSERT INTO ledger`
// has no ON CONFLICT and `ledger_company_id_name_key` is a plain UNIQUE index, so
// creating an existing ledger is a raw Postgres unique-violation surfaced as
// "masters: insert ledger: ERROR: duplicate key value violates unique constraint".
//
// The consequence: on a FRESH PAGE LOAD the memo is empty, so keying a Payment /
// Receipt / Contra against any ledger the company already had — the normal case, and
// every case after the first day — failed with that SQL error. It appeared to work
// only when the memo was warm from an earlier post in the same session.
//
// ⚠️ NOT ONE TEST IN THE SUITE CAUGHT IT, because they all run against the mock,
// whose createLedger happily mints a fresh id per call. It was found by driving the
// app. So these tests assert against a client whose createLedger REFUSES a duplicate
// the way the server does — which is the only shape that could have caught it.
// ---------------------------------------------------------------------------
describe("ensureLedger resolves an EXISTING ledger instead of re-creating it", () => {
  /** A client that refuses a duplicate name, as the server's UNIQUE index does. */
  function strictClient(existing: { id: string; name: string }[]) {
    let creates = 0;
    const rows = [...existing];
    const client = {
      ...createMockClient(),
      async listLedgers() {
        return {
          ledgers: rows.map((r) => ({
            id: r.id,
            name: r.name,
            group_name: "Indirect Expenses",
            opening_balance: "0.0000",
            opening_dc: "D",
            maintain_bill_by_bill: false,
            gstin: "",
            state_code: "",
            registration_type: "unregistered",
          })),
        } as never;
      },
      async createLedger(_cid: string, req: { name: string }) {
        creates++;
        if (rows.some((r) => r.name === req.name)) {
          // EXACTLY what cored does, message and all.
          throw new ApiError(
            500,
            'masters: insert ledger: ERROR: duplicate key value violates unique constraint "ledger_company_id_name_key" (SQLSTATE 23505)',
          );
        }
        const created = { id: `new-${req.name}`, name: req.name };
        rows.push(created);
        return { ledger_id: created.id } as never;
      },
    } as unknown as CoredClient;
    return { client, creates: () => creates };
  }

  it("⛔ returns the existing id and NEVER calls createLedger", async () => {
    _resetLedgerCache();
    const { client, creates } = strictClient([
      { id: "led-electricity", name: "Electricity" },
    ]);
    const id = await ensureLedger(client, "c1", {
      name: "Electricity",
      groupName: "Indirect Expenses",
    });
    expect(id).toBe("led-electricity");
    // THE ASSERTION THAT WOULD HAVE CAUGHT THE BUG: not one create attempt.
    expect(creates()).toBe(0);
  });

  it("still CREATES a ledger the company genuinely does not have", async () => {
    // The control. Without it, "never create" would pass by never working at all.
    _resetLedgerCache();
    const { client, creates } = strictClient([
      { id: "led-electricity", name: "Electricity" },
    ]);
    const id = await ensureLedger(client, "c1", {
      name: "Stationery",
      groupName: "Indirect Expenses",
    });
    expect(id).toBe("new-Stationery");
    expect(creates()).toBe(1);
  });

  // -------------------------------------------------------------------------
  // ⛔⛔ ensureOutputTaxLedgers — THE SAME BUG, ONE FUNCTION DOWN, FOUND BY A
  // BROWSER AFTER THE ONE ABOVE WAS ALREADY FIXED.
  //
  // The correction that made ensureLedger resolve-then-create left its
  // neighbour calling createLedger UNCONDITIONALLY for all three tax ledgers.
  // Against cored that is a unique violation; against the mock, whose
  // createLedger mints a fresh id per call, it is a success. So keying an ITEM
  // INVOICE on any company that had already posted one GST voucher failed in a
  // browser while 726 tests stayed green.
  //
  // These two tests are the ones that would have caught it, and they are here
  // rather than in a new file so the next person fixing an `ensure*` helper
  // sees BOTH call sites in one place.
  // -------------------------------------------------------------------------
  it("⛔ ensureOutputTaxLedgers RESOLVES the three tax ledgers and creates NONE", async () => {
    _resetLedgerCache();
    _resetOutputTaxLedgerCache();
    const { client, creates } = strictClient([
      { id: "led-cgst", name: "Output CGST" },
      { id: "led-sgst", name: "Output SGST" },
      { id: "led-igst", name: "Output IGST" },
    ]);
    const got = await ensureOutputTaxLedgers(client, "c1");
    expect(got).toEqual({
      cgstId: "led-cgst",
      sgstId: "led-sgst",
      igstId: "led-igst",
    });
    // THE ASSERTION THAT WOULD HAVE CAUGHT IT: not one create attempt against a
    // company that already has all three.
    expect(creates()).toBe(0);
  });

  it("still creates the tax ledgers a fresh company genuinely lacks", async () => {
    // The control, and it is the inverse of the assertion above: without it,
    // "never create" would pass by never working at all.
    _resetLedgerCache();
    _resetOutputTaxLedgerCache();
    const { client, creates } = strictClient([
      { id: "led-cgst", name: "Output CGST" },
    ]);
    const got = await ensureOutputTaxLedgers(client, "c1");
    expect(got.cgstId).toBe("led-cgst");
    expect(got.sgstId).toBe("new-Output SGST");
    expect(got.igstId).toBe("new-Output IGST");
    // Exactly the two that were missing, and not the one that was not.
    expect(creates()).toBe(2);
  });

  it("resolves two particulars naming the SAME new ledger to one id, with one create", async () => {
    _resetLedgerCache();
    const { client, creates } = strictClient([]);
    const a = await ensureLedger(client, "c1", {
      name: "Courier",
      groupName: "Indirect Expenses",
    });
    const b = await ensureLedger(client, "c1", {
      name: "Courier",
      groupName: "Indirect Expenses",
    });
    expect(a).toBe(b);
    expect(creates()).toBe(1);
  });

  it("matches the name EXACTLY, mirroring the UNIQUE index", async () => {
    // "electricity" and "Electricity" are two different ledgers to (company_id, name),
    // so resolving them to one here would make this helper disagree with the database
    // about what a duplicate is.
    _resetLedgerCache();
    const { client } = strictClient([{ id: "led-e", name: "Electricity" }]);
    const id = await ensureLedger(client, "c1", {
      name: "electricity",
      groupName: "Indirect Expenses",
    });
    expect(id).toBe("new-electricity");
  });
});

/**
 * ⛔⛔ THE ORIENTATION TABLE — WHICH WAY THE MONEY GOES, PER CLASS.
 *
 * This file had FIVE tests of `buildGstVoucherRequest` and every one of them
 * built a SALES voucher, so the builder's total blindness to the voucher class
 * was invisible here. It was found by keying a purchase in a browser and reading
 * `posting` in Postgres against the seeded controls (HANDOFF §5.5):
 *
 *   Purchase (seeded, CORRECT)  Cr supplier / Dr Purchases      / Dr INPUT tax
 *   Purchase (BROWSER-KEYED)    Dr supplier / Cr Purchases      / Cr OUTPUT tax
 *
 * ⚠️ TOTALS PROVE NOTHING HERE AND NEITHER DOES THE SERVER. Both orientations
 * balance; `validatePostings` only checks Dr == Cr; `deriveAndVerifyGST` sums
 * amounts WITHOUT reading `dr_cr` and says so in its own comment. Every
 * assertion below therefore names the SIDE and the LEDGER, never a sum.
 */
describe("⛔ the four GST classes each post their own way", () => {
  const build = (voucherType: string) =>
    buildGstVoucherRequest({
      voucherType,
      partyLedgerId: "party-1",
      incomeLedgerId: "base-1",
      taxableValue: "10000.0000",
      supplierStateCode: "29",
      gst: { ...emptyGstFormState(), placeOfSupply: "29", gstRatePct: "18" },
      outputTax: OUTPUT,
      voucherDate: "2026-05-10",
      party: "Acme Corp",
    });

  const sideOf = (req: ReturnType<typeof build>, ledgerId: string) =>
    req.postings!.find((p) => p.ledger_id === ledgerId)!.dr_cr;

  // party / base / tax — the three legs, per class. A credit note is the exact
  // reverse of a sale; a debit note the exact reverse of a purchase.
  const CASES: [string, "DR" | "CR", "DR" | "CR", "DR" | "CR"][] = [
    ["sales", "DR", "CR", "CR"],
    ["purchase", "CR", "DR", "DR"],
    ["credit-note", "CR", "DR", "DR"],
    ["debit-note", "DR", "CR", "CR"],
  ];

  for (const [cls, party, base, tax] of CASES) {
    it(`${cls}: party ${party} (gross), base ${base}, tax ${tax}`, () => {
      const req = build(cls);
      expect(sideOf(req, "party-1"), `${cls}: the PARTY leg`).toBe(party);
      expect(sideOf(req, "base-1"), `${cls}: the BASE leg`).toBe(base);
      expect(sideOf(req, "led-cgst"), `${cls}: the TAX legs`).toBe(tax);
      expect(sideOf(req, "led-sgst"), `${cls}: the TAX legs`).toBe(tax);
      // The gross is on the party leg and the base on the income leg — a swap
      // would also balance.
      const partyLeg = req.postings!.find((p) => p.ledger_id === "party-1")!;
      const baseLeg = req.postings!.find((p) => p.ledger_id === "base-1")!;
      expect(partyLeg.amount).toBe("11800.0000");
      expect(baseLeg.amount).toBe("10000.0000");
    });
  }

  it("⛔ a CREDIT NOTE is the exact mirror of the SALE it reverses", () => {
    // Stated as its own test because this is the operator-visible consequence:
    // before the fix, raising a sales return INCREASED revenue, output tax and
    // the customer's receivable by the amount it was raised to reduce them.
    const sale = build("sales");
    const note = build("credit-note");
    for (const id of ["party-1", "base-1", "led-cgst", "led-sgst"]) {
      expect(sideOf(note, id), `leg ${id} must reverse`).not.toBe(
        sideOf(sale, id),
      );
    }
  });

  it("⛔ a DEBIT NOTE is the exact mirror of the PURCHASE it reverses", () => {
    const purchase = build("purchase");
    const note = build("debit-note");
    for (const id of ["party-1", "base-1", "led-cgst", "led-sgst"]) {
      expect(sideOf(note, id), `leg ${id} must reverse`).not.toBe(
        sideOf(purchase, id),
      );
    }
  });

  it("⛔ the BILL REFERENCE follows the party leg, not a hard-coded DR", () => {
    // Left hard-coded, a purchase opened a RECEIVABLE against its supplier. The
    // server's sum-to-posting invariant refuses an allocation whose side
    // disagrees with the posting it belongs to, so this also 422s in production.
    const req = buildGstVoucherRequest({
      voucherType: "purchase",
      partyLedgerId: "party-1",
      incomeLedgerId: "base-1",
      taxableValue: "10000.0000",
      supplierStateCode: "29",
      gst: { ...emptyGstFormState(), placeOfSupply: "29", gstRatePct: "18" },
      outputTax: OUTPUT,
      voucherDate: "2026-05-10",
      party: "Acme Corp",
      billRefs: [
        { refType: "NEW", refName: "", amount: "11800.0000", dueDate: "" },
      ],
    });
    expect(req.bill_allocations![0]!.dr_cr).toBe("CR");
  });

  it("⛔ an UNKNOWN class THROWS rather than defaulting to the sales shape", () => {
    // The defect was a silent fallback. A class with no row is a programming
    // error and must be loud: guessing books a payable as a receivable, and it
    // balances, so nothing downstream would ever report it.
    expect(() => build("receipt")).toThrow(/no posting orientation/i);
  });
});

describe("⛔ tax ledgers are resolved per KIND, and the cache does not confuse them", () => {
  it("output and input resolve to DIFFERENT ledgers on the same company", async () => {
    const c = createMockClient({ seed: false });
    const { company_id } = await c.createCompany({
      name: "Orientation Co",
      fy_start_date: "2026-04-01",
      base_currency: "INR",
      state_code: "29",
    });
    // ⛔ THE ORDER MATTERS AND IS THE POINT. The memo was keyed on the company
    // alone, so whichever kind was asked for FIRST would be handed back for the
    // other — and Input-for-Output balances perfectly.
    const input = await ensureGstTaxLedgers(c, company_id, "input");
    const output = await ensureGstTaxLedgers(c, company_id, "output");
    expect(output.cgstId).not.toBe(input.cgstId);
    expect(output.sgstId).not.toBe(input.sgstId);
    expect(output.igstId).not.toBe(input.igstId);

    const nameOf = async (id: string) =>
      (await c.listLedgers(company_id)).ledgers.find((l) => l.id === id)
        ?.name;
    expect(await nameOf(output.cgstId)).toBe("Output CGST");
    expect(await nameOf(input.cgstId)).toBe("Input CGST");
  });
});

/* ===========================================================================
 * ⛔ THE ADDITIONAL CHARGE LINE — register row D3's collection half.
 * ===========================================================================
 * The invariant every test below defends is one sentence: A CHARGE IS MONEY THE
 * PARTY OWES AND IS NOT A SUPPLY. So it enters the party leg and the bill
 * reference, sits on the tax SIDE, and is absent from `taxable_value` — and
 * getting any one of those wrong still BALANCES, which is why each is asserted
 * on its own rather than inferred from the others.
 */
describe("⛔ D3: an additional charge is billed to the party and is NOT a supply", () => {
  const sale = (charges?: readonly { ledgerId: string; amount: string }[]) =>
    buildGstVoucherRequest({
      voucherType: "sales",
      partyLedgerId: "led-party",
      incomeLedgerId: "led-sales",
      taxableValue: "100000.0000",
      supplierStateCode: "29",
      gst: { ...emptyGstFormState(), placeOfSupply: "29", gstRatePct: "18", hsn: "7204" },
      outputTax: OUTPUT,
      ...(charges ? { charges } : {}),
    });

  it("with no charge it posts exactly what it always did — the CONTROL", () => {
    const req = sale();
    expect(req.postings!).toHaveLength(4);
    expect(req.postings![0]!.amount).toBe("118000.0000");
  });

  it("the PARTY leg carries it: 100000 + 18000 GST + 2000 charge = 120000", () => {
    const req = sale([{ ledgerId: "led-tcs", amount: "2000.0000" }]);
    const party = req.postings!.find((p) => p.ledger_id === "led-party")!;
    expect(party.amount).toBe("120000.0000");
    expect(party.dr_cr).toBe("DR");
  });

  it("⛔ it is NOT in the taxable value — folding it in would raise the GST on it", () => {
    const req = sale([{ ledgerId: "led-tcs", amount: "2000.0000" }]);
    expect(req.gst!.lines![0]!.taxable_value).toBe("100000.0000");
    // The tax is still 18% of the GOODS, not of the goods plus the charge.
    const cgst = req.postings!.find((p) => p.ledger_id === "led-cgst")!;
    expect(cgst.amount).toBe("9000.0000");
  });

  it("⛔ it sits on the TAX side, not the BASE side — it is not revenue", () => {
    // Credited to the charge account, never to Domestic Sales: booking it on
    // the base side would restate revenue the company did not earn, and the
    // voucher would balance either way.
    const req = sale([{ ledgerId: "led-tcs", amount: "2000.0000" }]);
    const charge = req.postings!.find((p) => p.ledger_id === "led-tcs")!;
    expect(charge.dr_cr).toBe("CR");
    const base = req.postings!.find((p) => p.ledger_id === "led-sales")!;
    expect(base.amount).toBe("100000.0000");
  });

  it("⛔ THE SIDE IS THE CLASS'S: a credit note REVERSES the charge with everything else", () => {
    const req = buildGstVoucherRequest({
      voucherType: "credit-note",
      partyLedgerId: "led-party",
      incomeLedgerId: "led-sales",
      taxableValue: "100000.0000",
      supplierStateCode: "29",
      gst: { ...emptyGstFormState(), placeOfSupply: "29", gstRatePct: "18" },
      outputTax: OUTPUT,
      charges: [{ ledgerId: "led-tcs", amount: "2000.0000" }],
    });
    // A sales return: the customer is CREDITED the whole 120000 back, and the
    // charge account is DEBITED — the exact reverse of the invoice.
    expect(req.postings!.find((p) => p.ledger_id === "led-party")!.dr_cr).toBe("CR");
    expect(req.postings!.find((p) => p.ledger_id === "led-party")!.amount).toBe("120000.0000");
    expect(req.postings!.find((p) => p.ledger_id === "led-tcs")!.dr_cr).toBe("DR");
  });

  it("⛔ THE BILL REFERENCE FOLLOWS THE PARTY LEG, charge included", () => {
    // The server refuses an allocation whose total disagrees with the posting it
    // belongs to, to the paisa — so referencing the PRE-charge figure would make
    // every bill-by-bill invoice carrying a charge unpostable.
    const req = buildGstVoucherRequest({
      voucherType: "sales",
      partyLedgerId: "led-party",
      incomeLedgerId: "led-sales",
      taxableValue: "100000.0000",
      supplierStateCode: "29",
      gst: { ...emptyGstFormState(), placeOfSupply: "29", gstRatePct: "18" },
      outputTax: OUTPUT,
      charges: [{ ledgerId: "led-tcs", amount: "2000.0000" }],
      billRefs: [{ refType: "NEW", refName: "", amount: "120000.0000", dueDate: "" }],
    });
    expect(req.bill_allocations).toHaveLength(1);
    expect(req.bill_allocations![0]!.amount).toBe("120000.0000");
    expect(req.bill_allocations![0]!.dr_cr).toBe("DR");
  });

  it("several charges all land, and all of them reach the party", () => {
    const req = sale([
      { ledgerId: "led-tcs", amount: "1000.0000" },
      { ledgerId: "led-freight", amount: "250.0000" },
    ]);
    expect(req.postings!.find((p) => p.ledger_id === "led-party")!.amount).toBe("119250.0000");
    // Sales + CGST + SGST + the two charges — five credits against one debit.
    expect(req.postings!.filter((p) => p.dr_cr === "CR")).toHaveLength(5);
  });
});

describe("⛔ D3: a half-keyed charge is REFUSED, never dropped", () => {
  const line = (ledger: string, amount: string) => [{ ledger, amount }];
  it("an untouched line is not a charge at all", () => {
    expect(chargeLinesRefusal(line("", ""))).toBe("");
    expect(keyedChargeLines(line("", ""))).toEqual([]);
  });
  it("an amount with no ledger has nowhere to be booked", () => {
    expect(chargeLinesRefusal(line("", "2000"))).toMatch(/names no ledger/i);
  });
  it("a ledger with no amount is refused rather than posted as zero", () => {
    expect(chargeLinesRefusal(line("TCS Payable", ""))).toMatch(/is empty/i);
  });
  it("a non-numeric amount is refused before it can reach the server", () => {
    expect(chargeLinesRefusal(line("TCS Payable", "two thousand"))).toMatch(
      /not a figure/i,
    );
  });
  it("⛔ a charge of ZERO is refused — a line saying the party owes nothing extra", () => {
    expect(chargeLinesRefusal(line("TCS Payable", "0"))).toMatch(/charges 0/i);
  });
  it("a complete line is accepted and survives keyedChargeLines", () => {
    expect(chargeLinesRefusal(line("TCS Payable", "2000"))).toBe("");
    expect(keyedChargeLines(line("TCS Payable", "2000"))).toHaveLength(1);
  });
  it("the refusal NAMES the line, because an operator with three charges must know which", () => {
    expect(
      chargeLinesRefusal([
        { ledger: "Freight", amount: "500" },
        { ledger: "", amount: "2000" },
      ]),
    ).toMatch(/line 2/i);
  });
});
