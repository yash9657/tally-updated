import { describe, expect, it } from "vitest";

import { billRefsFromView, buildBillAllocations } from "./billAllocation";
import { doubleEntryForexRefusal, doubleEntryLinesFromView } from "./doubleEntry";
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

/**
 * ⛔⛔ T8 STEP 8 — THE **DOUBLE-ENTRY GRID's** OWN ROUND TRIP, AND IT WAS BROKEN.
 *
 * §6.1 of `HANDOFF-T8-STEPS-6-TO-8.md` tells step 8 to ask, before shipping a form:
 * *"what does an alter through this form omit?"* — and names the answer in advance:
 * *"A currency field that does not round-trip the triple is defect number nine."*
 *
 * ⛔ IT WAS ALREADY DEFECT NUMBER NINE BEFORE THE FIELD EXISTED. The tests above
 * cover the SINGLE-ENTRY path (`postingsFromView`) and the BILL REFERENCES
 * (`billRefsFromView`). The DOUBLE-ENTRY grid has a third, independent hop —
 *
 *     PostingView  ->  doubleEntryLinesFromView  ->  DoubleEntryLine
 *                  ->  buildDoubleEntryRequest    ->  PostingRequest
 *
 * — and `DoubleEntryLine` did not carry the triple at all. So opening a foreign
 * JOURNAL (F7 is ALWAYS this grid), or a foreign Receipt in Double Entry mode,
 * fixing the narration and saving wrote a RUPEE voucher over it.
 *
 * ⚠️ AND NOTHING REFUSED IT, which is why it needed a test rather than a bug
 * report: the rebuilt voucher still balances in base, the double-entry guard is
 * satisfied, the Balance Sheet does not move by a paisa, and the only trace is that
 * the bill quietly stops being foreign — leaving the unadjusted-forex report and
 * realising no gain when it is settled.
 *
 * ⭐ MEASURED BEFORE THE FIX, with the triple removed from the type:
 *     currencyId  undefined  (wanted "cur-usd")
 *     foreignAmount undefined  (wanted "1000.0000")
 *     rate        undefined  (wanted "83.6512345678")
 */
describe("T8/8: the DOUBLE-ENTRY grid's own forex round trip", () => {
  const foreignPosting = {
    ledger: "Globex LLC",
    ledger_id: "led-globex",
    dr_cr: "D",
    amount: "83651.2300",
    currency_id: "cur-usd",
    currency: "USD",
    foreign_amount: "1000.0000",
    rate: "83.6512345678",
  };

  it("hydrates a foreign posting into a grid line carrying ALL THREE values", () => {
    const [line] = doubleEntryLinesFromView(
      [foreignPosting],
      () => [],
      () => [],
    );
    expect(line?.currencyId).toBe("cur-usd");
    expect(line?.foreignAmount).toBe("1000.0000");
    // ⛔ TEN DECIMALS, PASSED THROUGH UNTOUCHED. Re-formatting it with this
    // module's money helpers would hand back 83.6512 and reprice the bill.
    expect(line?.rate).toBe("83.6512345678");
    // Display only, so a picker can label the field without resolving the uuid.
    expect(line?.currencyCode).toBe("USD");
  });

  it("CONTROL: a base posting hydrates with NO forex keys at all, not empty ones", () => {
    const [line] = doubleEntryLinesFromView(
      [
        {
          ledger: "Bharat Traders",
          ledger_id: "led-bharat",
          dr_cr: "D",
          amount: "50000.0000",
        },
      ],
      () => [],
      () => [],
    );
    // ⛔ ABSENT, NOT "". `buildDoubleEntryRequest` keys the whole triple off the
    // currency being non-empty, and an empty-string currency would send a
    // two-of-three row that 0080's CHECK refuses — a 422 on a request that looks
    // complete on this side.
    expect(line?.currencyId).toBeUndefined();
    expect(line?.foreignAmount).toBeUndefined();
    expect(line?.rate).toBeUndefined();
    // And the control that says the hydration ran at all.
    expect(line?.amount).toBe("50000.0000");
    expect(line?.ledger).toBe("Bharat Traders");
  });

  it("refuses a HALF-KEYED triple, naming which value is missing", () => {
    const base = {
      ledger: "Globex LLC",
      side: "DR" as const,
      amount: "83651.23",
      allocations: [],
      billRefs: [],
    };
    // ⛔ ALL FOUR HALF-KEYED SHAPES, because 0080's CHECK refuses every one of
    // them and a form that caught three would send the fourth.
    expect(
      doubleEntryForexRefusal([{ ...base, foreignAmount: "1000" }]),
    ).toMatch(/no CURRENCY/);
    expect(
      doubleEntryForexRefusal([{ ...base, currencyId: "cur-usd" }]),
    ).toMatch(/no FOREIGN AMOUNT/);
    expect(
      doubleEntryForexRefusal([
        { ...base, currencyId: "cur-usd", foreignAmount: "1000" },
      ]),
    ).toMatch(/no EXCHANGE RATE/);
    expect(
      doubleEntryForexRefusal([
        { ...base, currencyId: "cur-usd", foreignAmount: "-5", rate: "83.65" },
      ]),
    ).toMatch(/negative foreign amount/);
  });

  it("CONTROL: a complete triple and a base line are both accepted", () => {
    const base = {
      ledger: "Globex LLC",
      side: "DR" as const,
      amount: "83651.23",
      allocations: [],
      billRefs: [],
    };
    expect(
      doubleEntryForexRefusal([
        {
          ...base,
          currencyId: "cur-usd",
          foreignAmount: "1000.00",
          rate: "83.6512345678",
        },
      ]),
    ).toBe("");
    expect(doubleEntryForexRefusal([base])).toBe("");
    // ⭐ AND AN UNKEYED ROW IS NOT JUDGED. The grid always offers one more line,
    // and a refusal about a blank row would make the form unusable.
    expect(
      doubleEntryForexRefusal([{ ...base, ledger: "", amount: "" }]),
    ).toBe("");
  });
});
