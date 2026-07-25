import { describe, it, expect } from "vitest";
import {
  applyDraftEdits,
  canRebuildDraft,
  DraftRebuildError,
  draftBodyToVoucherRequest,
} from "./draftAccept";
import type { PostingRequest, VoucherDraftBody } from "../../api";

/**
 * WS8b pure edit-through tests. The oracles are INDEPENDENT: hand-written money
 * literals (never re-running the SUT's own derivation), a debit==credit sum
 * computed in the test from the captured request, and the assertion that the
 * rebuilt tax legs carry the draft's OWN Input-tax ledger ids (never the Output
 * ids buildGstVoucherRequest would create).
 */

/** An intra-state @18 purchase draft: expense Dr 1000 + Input CGST/SGST Dr 90 + party Cr 1180. */
function intraDraftBody(): VoucherDraftBody {
  return {
    voucher_class: "purchase",
    party_ledger_id: "party-1",
    party: "Steel Suppliers",
    voucher_no: "STL/044",
    voucher_date: "2026-06-10",
    narration: "Steel rods",
    postings: [
      { ledger_id: "exp-1", ledger_name: "Purchase - Raw Material", dr_cr: "D", amount: "1000.0000" },
      { ledger_id: "cgst-1", ledger_name: "Input CGST", dr_cr: "D", amount: "90.0000" },
      { ledger_id: "sgst-1", ledger_name: "Input SGST", dr_cr: "D", amount: "90.0000" },
      { ledger_id: "party-1", ledger_name: "Steel Suppliers", dr_cr: "C", amount: "1180.0000" },
    ],
    gst: {
      ruleset_version: "2024.1",
      supplier_state_code: "29",
      place_of_supply_state_code: "29",
      reverse_charge: false,
      exempt: false,
      lines: [{ taxable_value: "1000.0000", gst_rate_pct: "18", hsn: "7214" }],
      cgst_ledger_id: "cgst-1",
      sgst_ledger_id: "sgst-1",
      igst_ledger_id: "igst-1",
    },
  };
}

function sumBy(postings: PostingRequest[], dc: "D" | "C"): number {
  return postings
    .filter((p) => p.dr_cr === dc)
    .reduce((acc, p) => acc + Math.round(Number.parseFloat(p.amount) * 10000), 0);
}

/** The rebuilt request's postings, indexed by ledger id (with the array too). */
function reqPostings(body: VoucherDraftBody, edits: Parameters<typeof applyDraftEdits>[1]) {
  const req = draftBodyToVoucherRequest(applyDraftEdits(body, edits));
  const postings = req.postings ?? [];
  const byId = Object.fromEntries(postings.map((p) => [p.ledger_id, p]));
  return { req, postings, byId };
}

describe("applyDraftEdits (WS8b per-field edit-through)", () => {
  it("rebuilds a taxable-value edit with the PURCHASE orientation preserved", () => {
    const { req, postings, byId } = reqPostings(intraDraftBody(), { taxableValue: "2000" });

    // Independent literals: taxable 2000 @18 intra -> CGST 180, SGST 180, total 2360.
    expect(byId["exp-1"]).toMatchObject({ dr_cr: "D", amount: "2000.0000" });
    expect(byId["cgst-1"]).toMatchObject({ dr_cr: "D", amount: "180.0000" });
    expect(byId["sgst-1"]).toMatchObject({ dr_cr: "D", amount: "180.0000" });
    expect(byId["party-1"]).toMatchObject({ dr_cr: "C", amount: "2360.0000" });

    // Orientation: expense + input tax are DEBITED; party is CREDITED (a purchase,
    // NOT the reversed sales shape buildGstVoucherRequest would emit).
    expect(postings.filter((p) => p.dr_cr === "D")).toHaveLength(3);
    expect(postings.filter((p) => p.dr_cr === "C")).toHaveLength(1);

    // The tax legs carry the draft's INPUT ledger ids, never Output ids.
    expect(byId["cgst-1"]?.ledger_id).toBe("cgst-1");
    expect(byId["sgst-1"]?.ledger_id).toBe("sgst-1");

    // Balances to the paisa (sum-of-debits == sum-of-credits), computed here.
    expect(sumBy(postings, "D")).toBe(sumBy(postings, "C"));

    // The gst block line reflects the edit; ids preserved.
    expect(req.gst?.lines?.[0]?.taxable_value).toBe("2000.0000");
    expect(req.gst?.cgst_ledger_id).toBe("cgst-1");
  });

  it("keeps amounts balanced when only the narration/date change", () => {
    const edited = applyDraftEdits(intraDraftBody(), { narration: "Edited note", voucherDate: "2026-06-12" });
    expect(edited.narration).toBe("Edited note");
    expect(edited.voucher_date).toBe("2026-06-12");
    // Amounts unchanged (taxable still 1000 -> 90/90/1180).
    const { postings, byId } = reqPostings(intraDraftBody(), { narration: "Edited note" });
    expect(byId["exp-1"]?.amount).toBe("1000.0000");
    expect(byId["party-1"]?.amount).toBe("1180.0000");
    expect(sumBy(postings, "D")).toBe(sumBy(postings, "C"));
  });

  it("derives intra/inter from supplier vs (edited) place of supply, not the home state", () => {
    // supplier 29, POS edited to 27 -> inter-state -> a SINGLE IGST leg (360 on 2000).
    const { req, postings, byId } = reqPostings(intraDraftBody(), { taxableValue: "2000", placeOfSupply: "27" });
    expect(byId["igst-1"]).toMatchObject({ dr_cr: "D", amount: "360.0000" });
    expect(byId["cgst-1"]).toBeUndefined();
    expect(byId["sgst-1"]).toBeUndefined();
    expect(byId["party-1"]).toMatchObject({ dr_cr: "C", amount: "2360.0000" });
    expect(req.gst?.supplier_state_code).toBe("29"); // vendor state preserved
    expect(req.gst?.place_of_supply_state_code).toBe("27");
    expect(sumBy(postings, "D")).toBe(sumBy(postings, "C"));
  });

  it("guards non-rebuildable shapes (multi-line / reverse-charge / exempt)", () => {
    const multi = intraDraftBody();
    multi.gst!.lines.push({ taxable_value: "500.0000", gst_rate_pct: "18" });
    expect(canRebuildDraft(multi)).toBe(false);
    expect(() => applyDraftEdits(multi, { taxableValue: "2000" })).toThrow(DraftRebuildError);

    const rcm = intraDraftBody();
    rcm.gst!.reverse_charge = true;
    expect(canRebuildDraft(rcm)).toBe(false);

    const exempt = intraDraftBody();
    exempt.gst!.exempt = true;
    expect(canRebuildDraft(exempt)).toBe(false);

    expect(canRebuildDraft(intraDraftBody())).toBe(true);
  });

  it("throws rather than silently create-under-the-wrong-group when a needed tax ledger id is absent", () => {
    const body = intraDraftBody();
    body.gst!.cgst_ledger_id = null; // intra needs CGST; its id is gone
    expect(() => applyDraftEdits(body, { taxableValue: "2000" })).toThrow(DraftRebuildError);
  });
});
