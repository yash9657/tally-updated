import { describe, it, expect } from "vitest";
import {
  buildBillAllocations,
  billRemainder,
  billRefusal,
  billAllocated,
  billRefsFromView,
  defaultNewRef,
  retrackAutoRef,
  emptyBillRef,
  type BillBearingLeg,
  type DraftBillRef,
} from "./billAllocation";
import type { BillAllocationView } from "../../api";

const leg: BillBearingLeg = {
  ledgerId: "party-1",
  drCr: "DR",
  amount: "94400.0000",
};

function ref(over: Partial<DraftBillRef> = {}): DraftBillRef {
  return { refType: "NEW", refName: "", amount: "94400.0000", dueDate: "", ...over };
}

describe("a NEW reference is sent UNNAMED, on purpose", () => {
  it("builds a reference with an empty ref_name", () => {
    const out = buildBillAllocations(leg, defaultNewRef(leg));
    expect(out).toEqual([
      {
        ledger_id: "party-1",
        ref_name: "",
        ref_type: "NEW",
        dr_cr: "DR",
        amount: "94400.0000",
      },
    ]);
  });

  it("does NOT invent a name, because a client-side guess would be permanent", () => {
    // The voucher number is allocated inside the post transaction. Anything this
    // module could put here — a timestamp, a uuid, the party's name — would be a
    // reference no human can tie back to a voucher, and it would be stored.
    const [only] = buildBillAllocations(leg, defaultNewRef(leg));
    expect(only!.ref_name).toBe("");
  });

  it("carries a due date only when one was keyed — an absent one is omitted, not empty", () => {
    const withDue = buildBillAllocations(leg, defaultNewRef(leg, "2026-08-30"));
    expect(withDue[0]!.due_date).toBe("2026-08-30");
    const without = buildBillAllocations(leg, defaultNewRef(leg));
    expect("due_date" in without[0]!).toBe(false);
  });

  it("defaults the whole leg, so the ordinary one-bill invoice needs no typing", () => {
    expect(defaultNewRef(leg)[0]!.amount).toBe("94400.0000");
    expect(billRefusal(leg, defaultNewRef(leg))).toBe("");
  });
});

describe("the defaulted reference re-tracks the invoice total", () => {
  // ⛔ THE ORDERING THAT BREAKS THE NAIVE VERSION: an operator picks the customer
  // FIRST and keys the value second, so a default captured at party-pick time is
  // 0.0000 — and the form would then refuse every invoice it had defaulted
  // itself. Caught by driving the real shell, not by reading the code.
  it("follows the leg while it is untouched", () => {
    const zero: BillBearingLeg = { ledgerId: "p", drCr: "DR", amount: "0.0000" };
    const defaulted = defaultNewRef(zero);
    expect(defaulted[0]!.amount).toBe("0.0000");
    expect(defaulted[0]!.auto).toBe(true);

    const grown = retrackAutoRef(defaulted, leg);
    expect(grown).not.toBeNull();
    expect(grown![0]!.amount).toBe("94400.0000");
    expect(billRefusal(leg, grown!)).toBe("");
  });

  it("STOPS following once the operator states an amount", () => {
    const keyed: DraftBillRef[] = [ref({ amount: "50000.0000", auto: false })];
    expect(retrackAutoRef(keyed, leg)).toBeNull();
  });

  it("never touches a SPLIT, however it was reached", () => {
    const split = [ref({ amount: "50000.0000" }), ref({ amount: "44400.0000" })];
    expect(retrackAutoRef(split, leg)).toBeNull();
  });

  it("is a no-op when it already agrees, so it cannot loop a render", () => {
    expect(retrackAutoRef(defaultNewRef(leg), leg)).toBeNull();
  });

  it("does not put `auto` on the wire — the server never sees it", () => {
    const built = buildBillAllocations(leg, defaultNewRef(leg));
    expect("auto" in built[0]!).toBe(false);
  });
});

describe("an AGAINST must name the bill it settles", () => {
  const receiptLeg: BillBearingLeg = { ledgerId: "party-1", drCr: "CR", amount: "50000.0000" };

  it("is DROPPED by the builder when unnamed, never sent blank", () => {
    // Sending it blank would let the server stamp this voucher's own number on
    // it, opening a SECOND bill instead of settling the invoice.
    const out = buildBillAllocations(receiptLeg, [
      ref({ refType: "AGAINST", refName: "", amount: "50000.0000" }),
    ]);
    expect(out).toEqual([]);
  });

  it("is REFUSED rather than silently dropped, so nothing vanishes on the way out", () => {
    const msg = billRefusal(receiptLeg, [
      ref({ refType: "AGAINST", refName: "", amount: "50000.0000" }),
    ]);
    expect(msg).toContain("does not say which bill it settles");
    // And it explains the trap: the arithmetic looks complete.
    expect(msg).toContain("looks complete");
  });

  it("is sent when it names a bill", () => {
    const out = buildBillAllocations(receiptLeg, [
      ref({ refType: "AGAINST", refName: "INV/0007", amount: "50000.0000" }),
    ]);
    expect(out).toEqual([
      {
        ledger_id: "party-1",
        ref_name: "INV/0007",
        ref_type: "AGAINST",
        dr_cr: "CR",
        amount: "50000.0000",
      },
    ]);
  });

  it("trims the name, because a trailing space silently opens a different bill", () => {
    // Outstandings groups by (ledger_id, ref_name) as stored. "INV/1 " and
    // "INV/1" are two bills, and the operator cannot see the difference.
    const out = buildBillAllocations(receiptLeg, [
      ref({ refType: "AGAINST", refName: "  INV/0007  ", amount: "50000.0000" }),
    ]);
    expect(out[0]!.ref_name).toBe("INV/0007");
  });
});

describe("the leg must be accounted for exactly, to the paisa", () => {
  it("accepts an exact total", () => {
    expect(billRefusal(leg, [ref()])).toBe("");
    expect(billRemainder(leg, [ref()])).toBe("0.0000");
  });

  it("refuses a SHORT reference and names the gap", () => {
    const msg = billRefusal(leg, [ref({ amount: "90000.0000" })]);
    expect(msg).toContain("short by 4400.0000");
    // The reason, not just the number: unreferenced money never ages.
    expect(msg).toContain("never reach the Outstandings report");
  });

  it("refuses an OVER-allocation and names the excess", () => {
    const msg = billRefusal(leg, [ref({ amount: "95000.0000" })]);
    expect(msg).toContain("over-allocated by 600.0000");
  });

  it("refuses a ONE-PAISA drift — there is no tolerance", () => {
    // Tolerances are how a real error hides for a year, and the server has none.
    expect(billRefusal(leg, [ref({ amount: "94400.0001" })])).not.toBe("");
    expect(billRefusal(leg, [ref({ amount: "94399.9999" })])).not.toBe("");
  });

  it("adds up a SPLIT across several bills exactly", () => {
    const refs = [
      ref({ amount: "50000.0000" }),
      ref({ amount: "44400.0000" }),
    ];
    expect(billAllocated(refs)).toBe("94400.0000");
    expect(billRefusal(leg, refs)).toBe("");
    expect(buildBillAllocations(leg, refs)).toHaveLength(2);
  });

  it("does not use floating point — a sum that drifts as a double is exact here", () => {
    // 0.1 + 0.2 !== 0.3 in IEEE754. This is the whole reason the module uses the
    // BigInt fixed-4 helpers rather than money.ts's parseFloat ones.
    const centLeg: BillBearingLeg = { ledgerId: "p", drCr: "DR", amount: "0.3000" };
    const refs = [ref({ amount: "0.1000" }), ref({ amount: "0.2000" })];
    expect(billAllocated(refs)).toBe("0.3000");
    expect(billRefusal(centLeg, refs)).toBe("");
  });

  it("treats NO references as legitimate — most ledgers are not bill-by-bill", () => {
    expect(billRefusal(leg, [])).toBe("");
  });

  it("drops a zero-amount row without refusing it", () => {
    // A blank row the operator added and did not fill is not an error.
    expect(buildBillAllocations(leg, [ref(), emptyBillRef()])).toHaveLength(1);
  });
});

describe("re-hydrating from a fetched voucher — the ALTER round trip", () => {
  const views: BillAllocationView[] = [
    {
      ledger_id: "party-1",
      ref_name: "INV/0007",
      ref_type: "NEW",
      dr_cr: "DR",
      amount: "94400.0000",
      due_date: "2026-08-30",
    },
    {
      ledger_id: "party-2",
      ref_name: "INV/0008",
      ref_type: "NEW",
      dr_cr: "DR",
      amount: "1000.0000",
      due_date: "",
    },
  ];

  it("rebuilds the references a voucher already carries", () => {
    expect(billRefsFromView(views, "party-1")).toEqual([
      { refType: "NEW", refName: "INV/0007", amount: "94400.0000", dueDate: "2026-08-30" },
    ]);
  });

  it("keeps the STORED name, so an alter settles the same bill it always did", () => {
    // ⛔ The stored name must survive verbatim. Re-blanking it on the way back
    // would let the server re-stamp, which is a no-op only by luck — and on a
    // voucher whose number changed it would silently open a new bill.
    const [only] = billRefsFromView(views, "party-1");
    expect(only!.refName).toBe("INV/0007");
    expect(buildBillAllocations(leg, billRefsFromView(views, "party-1"))[0]!.ref_name).toBe(
      "INV/0007",
    );
  });

  it("filters by ledger, because one voucher can reference two parties", () => {
    expect(billRefsFromView(views, "party-2")).toHaveLength(1);
    expect(billRefsFromView(views, "nobody")).toEqual([]);
  });

  it("survives a full read -> rebuild -> send round trip unchanged", () => {
    // This is the assertion that would have caught the live data-loss bug: a
    // form rebuilt from a GET must send back what it read.
    const rebuilt = buildBillAllocations(
      { ledgerId: "party-1", drCr: "DR", amount: "94400.0000" },
      billRefsFromView(views, "party-1"),
    );
    expect(rebuilt).toEqual([
      {
        ledger_id: "party-1",
        ref_name: "INV/0007",
        ref_type: "NEW",
        dr_cr: "DR",
        amount: "94400.0000",
        due_date: "2026-08-30",
      },
    ]);
  });
});
