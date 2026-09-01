import { describe, expect, it } from "vitest";

import { billRefsFromView, buildBillAllocations } from "./billAllocation";
import type { BillAllocationView, PostingView } from "../../api/types";

/**
 * forexRoundTrip.test.ts — T8 STEP 4, THE CLIENT HALF OF "AN ALTER RE-STATES".
 *
 * ===========================================================================
 * ⛔⛔ WHY A CLIENT TEST, WHEN THE SERVER ROUND-TRIPS CORRECTLY
 * ===========================================================================
 * Because the server round-tripping is NECESSARY AND NOT SUFFICIENT, and this
 * repository has now proved that six times. `voucher_readback_audit_test.go`'s
 * header records the lesson in one line: §4.3's original audit asked "does the
 * GET emit it?", ticked `bill_allocation` off, and cleared a table that was
 * actively losing data — because the GET emitted the rows and NO FORM EVER SENT
 * THEM BACK.
 *
 * So the question here is the client's own: does a request REBUILT FROM A
 * FETCHED VIEW carry the three values? The two functions below are the entire
 * path a bill reference takes on an alter —
 *
 *     BillAllocationView  ->  billRefsFromView  ->  DraftBillRef
 *                         ->  buildBillAllocations  ->  BillAllocationRequest
 *
 * — and a field missing from EITHER hop is a field the round trip drops, with
 * a correct server on both sides of it.
 *
 * ⛔ AND WHAT IS LOST HERE IS WORSE THAN A ROW. Every earlier occurrence of
 * this defect destroys a row: a cost split, an order link, a GST line — and a
 * missing row eventually shows up as a report that stops adding up. This one
 * destroys three VALUES on a row that survives. The posting is still there, the
 * base amount is still right, the voucher still balances, the Balance Sheet
 * does not move by a paisa, and a $1,000 bill has simply become a ₹83,651.23
 * bill: it leaves the unadjusted-forex report and realises no gain when it is
 * settled, because as far as the books are concerned it was never foreign.
 *
 * ⚠️ NO FORM FIELD EDITS THESE VALUES YET — there is no foreign-entry UI in
 * this step, by design (the memo's build order exposes the operator last). The
 * round trip still has to hold TODAY, because a foreign voucher can arrive
 * through the importer, a sync commit from an offline node, or the HTTP API,
 * and an operator opening it to fix a typo must not destroy it.
 */

const USD = "cur-usd-1";

function foreignBillView(): BillAllocationView {
  return {
    ledger_id: "party-1",
    ref_name: "IMP-1",
    ref_type: "NEW",
    dr_cr: "C",
    amount: "83651.2300",
    due_date: "2025-07-10",
    currency_id: USD,
    currency: "USD",
    foreign_amount: "1000.0000",
    // ⚠️ TEN DECIMALS, AND THE FIFTH ONWARD ARE NON-ZERO ON PURPOSE. A rate
    // that survives as "83.6500" would pass a test written with a round rate
    // even if every layer treated it as fixed-4 money.
    rate: "83.6512345678",
  };
}

const leg = { ledgerId: "party-1", drCr: "C" as const, amount: "83651.2300" };

describe("a foreign bill reference survives the view -> form -> request round trip", () => {
  it("carries ALL THREE values, each named separately", () => {
    const [ref] = billRefsFromView([foreignBillView()], "party-1");
    const [req] = buildBillAllocations(leg, [ref!]);

    // Named one at a time. A single deep-equal would report "the row is wrong"
    // for three different bugs, and the three have different causes and
    // different consequences.
    expect(req!.currency_id, "THE CURRENCY").toBe(USD);
    expect(req!.foreign_amount, "THE FOREIGN AMOUNT").toBe("1000.0000");
    expect(req!.rate, "THE RATE, at full scale").toBe("83.6512345678");

    // ⛔ AND THE BASE AMOUNT IS UNMOVED. It is authoritative and stored, never
    // re-derived from foreign × rate — which for these figures would give
    // 83651.2346 and quietly unbalance the voucher against its own other leg.
    expect(req!.amount, "THE BASE AMOUNT").toBe("83651.2300");
  });

  it("⛔ does NOT re-format the rate through the fixed-4 money path", () => {
    // The failure this guards is invisible by construction: 83.6512 is a
    // perfectly well-formed rate that round-trips through every later layer.
    const [ref] = billRefsFromView([foreignBillView()], "party-1");
    const [req] = buildBillAllocations(leg, [ref!]);
    expect(req!.rate).not.toBe("83.6512");
    expect(req!.rate).not.toBe("83.6500");
  });

  it("CONTROL: a base bill emits no forex keys AT ALL, not empty ones", () => {
    // ⛔ THIS IS NOT PEDANTRY. `currency_id: ""` is not "no currency" — it is a
    // two-of-three triple, which the server's all-or-none CHECK refuses. A
    // client that emitted empty keys would 422 on every rupee voucher in the
    // book, which is every voucher that exists today.
    const base: BillAllocationView = {
      ledger_id: "party-1",
      ref_name: "INV-1",
      ref_type: "NEW",
      dr_cr: "C",
      amount: "5000.0000",
      due_date: "",
    };
    const [ref] = billRefsFromView([base], "party-1");
    const [req] = buildBillAllocations(
      { ledgerId: "party-1", drCr: "C", amount: "5000.0000" },
      [ref!],
    );
    expect(req).not.toHaveProperty("currency_id");
    expect(req).not.toHaveProperty("foreign_amount");
    expect(req).not.toHaveProperty("rate");
  });

  it("emits all three or none — never a partial triple", () => {
    // A view whose currency arrived without the other two (which the server
    // cannot produce, but a hand-built body or a future mock could) must not
    // become a request that names a rate and no currency, or vice versa.
    const [ref] = billRefsFromView(
      [{ ...foreignBillView(), foreign_amount: undefined, rate: undefined }],
      "party-1",
    );
    const [req] = buildBillAllocations(leg, [ref!]);
    const present = ["currency_id", "foreign_amount", "rate"].filter((k) =>
      Object.prototype.hasOwnProperty.call(req!, k),
    );
    expect(
      present.length === 0 || present.length === 3,
      `emitted a partial triple: ${present.join(", ")}`,
    ).toBe(true);
  });
});

/**
 * The POSTING half of the same round trip.
 *
 * `postingsFromView` lives inside VoucherScreen.tsx and is not exported, so
 * this pins the SHAPE CONTRACT it has to satisfy — that a rebuild keyed off
 * `currency_id` carries all three and omits all three — against the same view
 * type the screen consumes. It is the weaker of the two guards in this file;
 * the strong one is the wire drive, which posts a foreign voucher, alters it
 * through the real HTTP route and reads the row back out of Postgres.
 */
describe("the posting view carries what a rebuild needs", () => {
  it("a foreign posting view exposes all three values", () => {
    const p: PostingView = {
      ledger_id: "party-1",
      ledger: "Acme Inc (USA)",
      dr_cr: "C",
      amount: "83651.2300",
      currency_id: USD,
      currency: "USD",
      foreign_amount: "1000.0000",
      rate: "83.6512345678",
    };
    expect(p.currency_id).toBe(USD);
    expect(p.foreign_amount).toBe("1000.0000");
    expect(p.rate).toBe("83.6512345678");
  });
});
