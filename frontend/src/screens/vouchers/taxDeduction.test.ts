import { describe, expect, it } from "vitest";

import {
  buildTCSRequest,
  buildTDSRequest,
  emptyTaxDeductionForm,
  isTaxDeductionKeyed,
  sectionSummary,
  taxDeductionRefusal,
  taxLegRefusal,
  unpostedTaxLegNotice,
  type TaxDeductionForm,
} from "./taxDeduction";
import { MOCK_TDS_SECTIONS, MOCK_TCS_SECTIONS } from "../../api/mock";

/**
 * ⛔ REGISTER ROW D3 — the pure half. A TDS or TCS deduction could not be keyed
 * from the keyboard ANYWHERE in this product: the wire carried it, the ledger
 * derived and stored it, the register read it and the challan mechanism depended
 * on it, and no form filled it — so on any books a real operator built there were
 * no deductions at all and the challan's allocation picker was empty.
 *
 * These tests are about the two things a form like this gets wrong: sending a
 * HALF-KEYED block (which the server refuses after the ledgers have been
 * created), and DEFAULTING a field whose default is a statement nobody made.
 */

/** No tax leg named: the request then carries no *_ledger_id and nothing is verified. */
const noLeg = () => "";
/** The grid's resolution, as the builder gets it from the postings it just made. */
const legIds: Record<string, string> = {
  "TDS Payable": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
};
const resolveLeg = (name: string) => legIds[name] ?? "";

function keyed(over: Partial<TaxDeductionForm> = {}): TaxDeductionForm {
  return {
    ...emptyTaxDeductionForm(),
    section: "194J-P",
    partyLedgerId: "11111111-2222-3333-4444-555555555555",
    partyName: "Chitra Consulting",
    taxableAmount: "200000",
    ...over,
  };
}

describe("⛔ D3: nothing is defaulted that is a statement about the payment", () => {
  it("a fresh block names NO section — the section decides the rate AND the return code", () => {
    expect(emptyTaxDeductionForm().section).toBe("");
  });

  it("PAN-available defaults to TRUE, because unticking it RAISES the rate", () => {
    // The safe direction on this field is the opposite of the usual one: a
    // default of false silently over-deducts from every party whose PAN the
    // operator never thought about.
    expect(emptyTaxDeductionForm().panAvailable).toBe(true);
  });

  it("an untouched block is not keyed, and sends nothing", () => {
    const f = emptyTaxDeductionForm();
    expect(isTaxDeductionKeyed(f)).toBe(false);
    expect(taxDeductionRefusal(f, "TDS")).toBe("");
    expect(buildTDSRequest(f, noLeg)).toBeUndefined();
    expect(buildTCSRequest(f, noLeg)).toBeUndefined();
  });
});

describe("⛔ D3: a HALF-KEYED block is refused here, not by the server after the fact", () => {
  it("no section", () => {
    const why = taxDeductionRefusal(keyed({ section: "" }), "TDS");
    expect(why).toContain("NO DEFAULT");
    expect(why).toContain("payment code");
    expect(buildTDSRequest(keyed({ section: "" }), noLeg)).toBeUndefined();
  });

  it("no deductee — half the key of the statutory aggregate", () => {
    const why = taxDeductionRefusal(keyed({ partyLedgerId: "" }), "TDS");
    expect(why).toContain("deductee");
    expect(buildTDSRequest(keyed({ partyLedgerId: "" }), noLeg)).toBeUndefined();
  });

  it("…and it says BUYER on the collection side, which is the word the return uses", () => {
    expect(taxDeductionRefusal(keyed({ partyLedgerId: "" }), "TCS")).toContain("buyer");
  });

  it("no amount", () => {
    expect(taxDeductionRefusal(keyed({ taxableAmount: "" }), "TDS")).toContain(
      "no amount",
    );
  });

  it("a NEGATIVE amount — a deduction is not a refund", () => {
    expect(taxDeductionRefusal(keyed({ taxableAmount: "-500" }), "TDS")).toContain(
      "MAGNITUDE",
    );
    expect(buildTDSRequest(keyed({ taxableAmount: "-500" }), noLeg)).toBeUndefined();
  });

  it("a COMPLETE block is not refused", () => {
    expect(taxDeductionRefusal(keyed(), "TDS")).toBe("");
  });
});

describe("⛔ D3: the request blocks are exactly what the wire takes", () => {
  it("the tds block carries the section, the deductee, the type, the PAN fact and the amount", () => {
    expect(buildTDSRequest(keyed(), noLeg)).toEqual({
      section: "194J-P",
      deductee_ledger_id: "11111111-2222-3333-4444-555555555555",
      deductee_type: "OTHER",
      pan_available: true,
      // NORMALISED to fixed-4 — money is a string end to end and "200000" and
      // "200000.0000" must not be two different amounts on the wire.
      taxable_amount: "200000.0000",
    });
  });

  it("⛔ it does NOT send tds_ledger_id", () => {
    // That field asks the server to cross-check a posted TDS leg against its own
    // derivation. This form posts no such leg, so sending it would ask for a
    // comparison against a posting that is not there and refuse the voucher for
    // a contradiction the operator did not create.
    expect(buildTDSRequest(keyed(), noLeg)).not.toHaveProperty("tds_ledger_id");
  });

  it("IND_HUF travels, because 194C's rate splits on it", () => {
    expect(buildTDSRequest(keyed({ section: "194C", deducteeType: "IND_HUF" }), noLeg)?.deductee_type)
      .toBe("IND_HUF");
  });

  it("the tcs block names the BUYER and carries NO exemption field", () => {
    const req = buildTCSRequest(keyed({ section: "206C(1)-SCRAP" }), noLeg);
    expect(req).toEqual({
      section: "206C(1)-SCRAP",
      buyer_ledger_id: "11111111-2222-3333-4444-555555555555",
      pan_available: true,
      taxable_amount: "200000.0000",
    });
    // A s.394(2) exemption rests on a WRITTEN DECLARATION the collector holds and
    // the post path looks it up. A flag here would let an operator zero the tax
    // on any sale by ticking a box.
    expect(req).not.toHaveProperty("declaration");
    expect(req).not.toHaveProperty("exempt");
  });
});

describe("⛔ D3: the rate shown is the SERVER'S, echoed — never computed here", () => {
  const tds194C = MOCK_TDS_SECTIONS.find((s) => s.section === "194C")!;
  const tdsNoPan = { kind: "TDS" as const, floorPct: "20" };
  const scrap = MOCK_TCS_SECTIONS.find((s) => s.section === "206C(1)-SCRAP")!;
  const tcsNoPan = {
    kind: "TCS" as const,
    multiplier: "2",
    floorPct: "5",
    ceilingPct: "20",
  };

  it("194C reads 2% for OTHER and 1% for IND_HUF — the only section that splits", () => {
    expect(sectionSummary(tds194C, keyed({ section: "194C" }), tdsNoPan)).toContain("2%");
    expect(
      sectionSummary(tds194C, keyed({ section: "194C", deducteeType: "IND_HUF" }), tdsNoPan),
    ).toContain("1%");
  });

  it("the threshold is stated as EXCEEDED, not reached", () => {
    // The statutes read "exceeding": an amount exactly equal to the limit
    // attracts no deduction, and a form that said "from 30000" would be wrong by
    // one payment.
    expect(sectionSummary(tds194C, keyed({ section: "194C" }), tdsNoPan)).toContain(
      "exactly equal to it attracts none",
    );
  });

  it("⛔ discloses that an ANNUAL limit is applied PER PAYMENT by this build", () => {
    const annual = MOCK_TDS_SECTIONS.find((s) => s.basis === "ANNUAL")!;
    expect(sectionSummary(annual, keyed({ section: annual.section }), tdsNoPan)).toContain(
      "under-deducts",
    );
  });

  it("a NIL-threshold section says so rather than printing 0.0000", () => {
    const nil = MOCK_TDS_SECTIONS.find((s) => s.basis === "NIL")!;
    expect(sectionSummary(nil, keyed({ section: nil.section }), tdsNoPan)).toContain(
      "no threshold",
    );
  });

  it("⛔ the two no-PAN rules read DIFFERENTLY, because they ARE different", () => {
    // Deduction: max(rate, 20%). Collection: min(max(2 × rate, 5%), 20%). A
    // screen that reused one sentence for both would tell a scrap collector 20%
    // where the answer is 5%.
    const noPanTDS = sectionSummary(tds194C, keyed({ section: "194C", panAvailable: false }), tdsNoPan);
    expect(noPanTDS).toContain("HIGHER of");
    expect(noPanTDS).toContain("20%");

    const noPanTCS = sectionSummary(
      scrap,
      keyed({ section: "206C(1)-SCRAP", panAvailable: false }),
      tcsNoPan,
    );
    expect(noPanTCS).toContain("2×");
    expect(noPanTCS).toContain("at least 5%");
    expect(noPanTCS).toContain("at most 20%");
    expect(noPanTCS).not.toContain("HIGHER of");
  });

  it("says the SERVER derives the tax, so the figure is never read as a computation", () => {
    expect(sectionSummary(tds194C, keyed({ section: "194C" }), tdsNoPan)).toContain(
      "The server derives the tax",
    );
  });
});

describe("⛔ D3: the withheld tax is a real posting, and the server is told which line it is", () => {
  it("names the leg, so the server VERIFIES it against its own derivation", () => {
    const req = buildTDSRequest(keyed({ taxLegLedger: "TDS Payable" }), resolveLeg);
    expect(req?.tds_ledger_id).toBe("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee");
  });

  it("the collection side names its leg too", () => {
    const req = buildTCSRequest(
      keyed({ section: "206C(1)-SCRAP", taxLegLedger: "TDS Payable" }),
      resolveLeg,
    );
    expect(req?.tcs_ledger_id).toBe("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee");
  });

  it("no leg named -> NO *_ledger_id, and the voucher is DISCLOSED as recording without posting", () => {
    // Legal and real — a company may book its withholdings by a separate journal
    // — so it is disclosed, not refused. What must not happen is the operator
    // not knowing that the books say the full sum left.
    const f = keyed();
    expect(buildTDSRequest(f, noLeg)).not.toHaveProperty("tds_ledger_id");
    const notice = unpostedTaxLegNotice(f, "TDS");
    expect(notice).toContain("full amount as paid out");
    expect(notice).toContain("recorded");
  });

  it("…and the notice is silent once a leg IS named", () => {
    expect(unpostedTaxLegNotice(keyed({ taxLegLedger: "TDS Payable" }), "TDS")).toBe("");
  });

  it("⛔ a named leg that is NOT on the grid is REFUSED, not silently dropped", () => {
    // tds_ledger_id pointing at a leg that is not posted makes the server's
    // verification loop sum ZERO and compare it against the derived tax: either
    // a mismatch the operator cannot explain, or — below the threshold — a pass
    // that verified nothing at all.
    const why = taxLegRefusal(keyed({ taxLegLedger: "TDS Payable" }), "TDS", [
      "Professional Fees",
      "HDFC Bank",
    ]);
    expect(why).toContain("no line on this voucher names that ledger");
    expect(why).toContain("silently does not happen");
  });

  it("…and is silent when the line IS on the grid, case-insensitively", () => {
    expect(
      taxLegRefusal(keyed({ taxLegLedger: "tds payable" }), "TDS", [
        "Professional Fees",
        "TDS Payable",
      ]),
    ).toBe("");
  });

  it("an untouched block refuses nothing and discloses nothing", () => {
    const f = emptyTaxDeductionForm();
    expect(taxLegRefusal(f, "TDS", [])).toBe("");
    expect(unpostedTaxLegNotice(f, "TDS")).toBe("");
  });
});
