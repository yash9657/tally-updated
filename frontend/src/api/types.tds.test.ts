import { describe, expect, it } from "vitest";

import type {
  AllocateToChallanRequest,
  CreateTDSChallanAllocation,
  Money,
  VoucherRequest,
} from "./types";

/**
 * THE WIRE CONTRACT FOR TDS/TCS — A TYPE TEST, AND IT IS NOT CEREMONY.
 *
 * ⛔ REGISTER ROW D3 WAS A MISSING FIELD ON THIS EXACT TYPE. `VoucherRequest`
 * had no `tds` and no `tcs`, while the Go wire, the ledger, the register and the
 * whole challan mechanism had carried them since T6 was wired — so on any books
 * a real operator keyed there were no deductions at all and the challan's
 * allocation picker was empty. It is the THIRD time this file has been the layer
 * that lagged (`item_ledger_id` and `cost_allocations` both carry a comment
 * describing the identical defect), and nothing failed any of those three times,
 * because every layer under test was fine.
 *
 * A unit test cannot catch that on its own — the defect is the ABSENCE of a
 * field, and absence compiles. What catches it is a body written the way a
 * screen would write it, checked by `tsc`: remove the field and this file stops
 * compiling, which is the only executable claim a pure type can make.
 *
 * ⚠️ THE RUNTIME ASSERTIONS BELOW ARE DELIBERATELY SHALLOW. They exist so the
 * file is a real test rather than a comment; the LOAD-BEARING half is that
 * `pnpm exec tsc --noEmit` must accept it. If you delete this file, delete the
 * comment in {@link VoucherRequest.tds} with it.
 */
describe("the TDS/TCS wire contract", () => {
  it("lets a voucher body carry a deduction, the way a screen would build one", () => {
    const req: VoucherRequest = {
      voucher_date: "2026-05-10",
      party_ledger_id: "3f1b2c3d-4e5f-4a6b-8c9d-0e1f2a3b4c5d",
      created_by: "asha",
      postings: [
        {
          ledger_id: "expense-ledger",
          dr_cr: "D",
          amount: "200000.0000" as Money,
        },
        {
          ledger_id: "tds-payable-ledger",
          dr_cr: "C",
          amount: "20000.0000" as Money,
        },
        {
          ledger_id: "3f1b2c3d-4e5f-4a6b-8c9d-0e1f2a3b4c5d",
          dr_cr: "C",
          amount: "180000.0000" as Money,
        },
      ],
      // ⛔ THE FIELD THAT DID NOT EXIST. Without it there is no keyable path to a
      // deduction anywhere in this product, so the TDS register is empty on any
      // real books and no challan can name anything.
      tds: {
        section: "194J-P",
        deductee_ledger_id: "3f1b2c3d-4e5f-4a6b-8c9d-0e1f2a3b4c5d",
        deductee_type: "OTHER",
        pan_available: true,
        taxable_amount: "200000.0000" as Money,
        tds_ledger_id: "tds-payable-ledger",
      },
    };
    expect(req.tds?.section).toBe("194J-P");
    // The engine derives the tax; the client never states a RATE. If a
    // `rate_pct` ever appears on this block, a client could tax a payment at
    // whatever it liked — which is the argument GSTLineRequest's absent
    // `hsn`-from-master carries one type up.
    expect(Object.keys(req.tds ?? {})).not.toContain("rate_pct");
  });

  it("lets a SALES body carry a collection, with no exemption flag on it", () => {
    const req: VoucherRequest = {
      voucher_date: "2026-07-12",
      party_ledger_id: "buyer-ledger",
      tcs: {
        section: "206C(1)-SCRAP",
        buyer_ledger_id: "buyer-ledger",
        pan_available: true,
        taxable_amount: "100000.0000" as Money,
      },
    };
    // ⛔ THE ABSENCE IS THE POINT. A s.394(2) exemption rests on a written
    // declaration the collector holds; the post path looks it up by (buyer,
    // section, tax year). A `declaration: true` flag here would let an operator
    // zero the tax on any sale by typing a boolean, with no document behind it.
    expect(Object.keys(req.tcs ?? {})).not.toContain("declaration");
    expect(Object.keys(req.tcs ?? {})).not.toContain("exempt");
  });

  it("lets an allocation name which side of the statute it pays", () => {
    // Served by the picker and sent back unchanged — the identifiers AND the
    // kind, because no human can type any of them.
    const tds: CreateTDSChallanAllocation = {
      voucher_id: "5c3e9d2a-1b4f-4d6e-8a90-11c2d3e4f501",
      section: "194J-P",
      leg_seq: 0,
      amount_rupees: "20000.0000" as Money,
    };
    const tcs: CreateTDSChallanAllocation = {
      ...tds,
      section: "206C(1)-SCRAP",
      kind: "TCS",
      amount_rupees: "2000.0000" as Money,
    };
    // An absent kind means TDS, which is what every allocation recorded before
    // the collection side existed is.
    expect(tds.kind).toBeUndefined();
    expect(tcs.kind).toBe("TCS");
  });

  it("can ask an ALREADY RECORDED deposit to cover one more deduction", () => {
    // ⛔ REGISTER ROW D1. Before this route the unallocated balance of a deposit
    // that had paid for more than it named could never fall — seven browser
    // doors and six HTTP methods, and 05130 stayed at 12000.0000.
    const req: AllocateToChallanRequest = {
      allocations: [
        {
          voucher_id: "5c3e9d2a-1b4f-4d6e-8a90-11c2d3e4f502",
          section: "194C",
          leg_seq: 0,
          amount_rupees: "12000.0000" as Money,
        },
      ],
      recorded_by: "asha",
    };
    expect(req.allocations).toHaveLength(1);
    // ⚠️ NO DATE ON THIS BODY, and that is deliberate: the deposit is already
    // recorded and an allocation adopts its date. The back-dated cutoff reads
    // the STORED deposit_date, so this request can be refused 403 without
    // carrying a date at all — a screen must surface the server's sentence
    // rather than assuming a 403 is an ACL problem.
    expect(Object.keys(req)).not.toContain("deposit_date");
  });
});

// ---------------------------------------------------------------------------
// ⛔ THE MOCK'S SECTION TABLES ARE THE ONLY COPY OF THE FINANCE ACT OUTSIDE GO.
//
// The entry form that closes register row D3 offers these sections, and its
// mock has no server to ask — so a hand-kept copy exists, and a hand-kept copy
// of a table that changes with every Finance Act is the drift bug the TDS
// challan screen's rule (3) records for three minor heads. Here it is pinned
// against the GOLDEN that internal/compliance emits from the ruleset itself
// (TestTaxSectionsGolden), so a section added in Go and forgotten here fails
// with a diff rather than shipping a picker that is missing a section.
// ---------------------------------------------------------------------------
import taxSectionsGolden from "./goldens/tax-sections.json";
import { createMockClient, MOCK_TDS_SECTIONS, MOCK_TCS_SECTIONS } from "./mock";

describe("⛔ D3: the served section vocabulary cannot drift from the ruleset", () => {
  it("the mock's TDS table IS the ruleset's, row for row", () => {
    expect(MOCK_TDS_SECTIONS).toEqual(taxSectionsGolden.tds_sections);
  });

  it("the mock's TCS table IS the ruleset's, row for row", () => {
    expect(MOCK_TCS_SECTIONS).toEqual(taxSectionsGolden.tcs_sections);
  });

  it("serves the whole response the wire serves — versions and both no-PAN rules", async () => {
    const res = await createMockClient({ seed: false }).taxSections("company-demo");
    expect(res).toEqual(taxSectionsGolden);
  });

  it("⛔ the two no-PAN rules are DIFFERENT SHAPES, and both are served", async () => {
    // Deduction: max(rate, floor). Collection: min(max(mul × rate, floor), ceiling).
    // A client that reused the deduction shape here would print 20% where the
    // answer for a 2% scrap collection with no PAN is 5%… — the reason both
    // sets of figures travel rather than one.
    const res = await createMockClient({ seed: false }).taxSections("company-demo");
    expect(res.tds_no_pan_floor_pct).toBe("20");
    expect(res.tcs_no_pan_multiplier).toBe("2");
    expect(res.tcs_no_pan_floor_pct).toBe("5");
    expect(res.tcs_no_pan_ceiling_pct).toBe("20");
  });

  it("discloses that v1 applies EVERY threshold basis per transaction", async () => {
    // The form shows a threshold and an operator reads it as the law. An ANNUAL
    // or MONTHLY limit applied per transaction under-deducts on a split payment.
    const res = await createMockClient({ seed: false }).taxSections("company-demo");
    expect(res.threshold_basis_is_approximated).toBe(true);
    expect(res.tds_sections.map((s) => s.basis)).toContain("ANNUAL");
  });
});
