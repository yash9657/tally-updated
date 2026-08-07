/**
 * Tests for demoLinks.
 *
 * ⛔ EVERY CASE HERE IS CHOSEN TO BE A DISTINGUISHING INPUT, not a
 * representative one. The question asked of each is "which input tells the
 * right implementation apart from the plausible wrong one?", because the two
 * defects this module exists to prevent — a cost split attached to the wrong
 * posting, and a fulfilment off by one — both produce data that is internally
 * consistent, balances, foots and reconciles.
 *
 * In particular: the two legs of a payment carry THE SAME AMOUNT. A fixture
 * where they differ would let `costSplitOn` pass while picking the leg by
 * amount, by position, or at random. So the postings fixtures below are
 * deliberately EQUAL-AMOUNT and distinguishable only by ledger.
 */

import { describe, expect, it } from "vitest";

import type { InventoryLineRequest, PostingRequest } from "../api";
import { costSplitOn, fulfilmentOn, scaled4 } from "./demoLinks";

const RENT = "11111111-1111-1111-1111-111111111111";
const BANK = "22222222-2222-2222-2222-222222222222";
const DEPARTMENT = "33333333-3333-3333-3333-333333333333";
const BRANCH = "34343434-3434-3434-3434-343434343434";
const SALES_CENTRE = "44444444-4444-4444-4444-444444444444";
const ADMIN_CENTRE = "55555555-5555-5555-5555-555555555555";

const ANGLE = "66666666-6666-6666-6666-666666666666";
const SHEET = "77777777-7777-7777-7777-777777777777";
const ORDER_LINE = "88888888-8888-8888-8888-888888888888";
const GODOWN = "99999999-9999-9999-9999-999999999999";

/** Expense leg FIRST. Both legs 45,000 — only the ledger tells them apart. */
const expenseFirst: PostingRequest[] = [
  { ledger_id: RENT, dr_cr: "D", amount: "45000.0000" },
  { ledger_id: BANK, dr_cr: "C", amount: "45000.0000" },
];

/**
 * Expense leg SECOND — the shape HANDOFF §1.2 warns about, where the single-
 * entry panel puts the account paid FROM at index 0 and the particular the
 * operator sees at row 0 is posting 1.
 */
const expenseSecond: PostingRequest[] = [
  { ledger_id: BANK, dr_cr: "C", amount: "45000.0000" },
  { ledger_id: RENT, dr_cr: "D", amount: "45000.0000" },
];

const line = (
  stockItemID: string,
  qty: string,
): InventoryLineRequest => ({
  stock_item_id: stockItemID,
  godown_id: GODOWN,
  direction: "OUT",
  qty,
  rate: "25.7500",
  amount: "0.0000",
});

describe("scaled4", () => {
  it("reads a fixed-4 string as exact ten-thousandths", () => {
    expect(scaled4("45000.0000")).toBe(450000000n);
    expect(scaled4("0.0001")).toBe(1n);
    expect(scaled4("-12.5")).toBe(-125000n);
    expect(scaled4("7")).toBe(70000n);
  });

  // The three spellings a float parser accepts and a ledger must not. Each of
  // these reaching decimal arithmetic is a different flavour of disaster:
  // NaN/Infinity panic inside the server's decimal package, and a fifth
  // decimal place has already lost data before it reaches the wire.
  it.each(["NaN", "Infinity", "1e4", "0.12345", "", " 1.00 ", "1,000.00"])(
    "refuses %j",
    (bad) => {
      expect(() => scaled4(bad)).toThrow(/fixed-point decimal string/);
    },
  );
});

describe("costSplitOn derives posting_seq from the LEDGER, not the amount", () => {
  // ⛔ THE DISTINGUISHING PAIR. Same amounts, same split, opposite orders. An
  // implementation that hardcodes 0, hardcodes the last index, or picks the
  // larger/first debit passes exactly one of these two and fails the other.
  it("finds the expense leg when it is FIRST", () => {
    const out = costSplitOn(expenseFirst, RENT, DEPARTMENT, [
      { costCentreID: SALES_CENTRE, amount: "27000.0000" },
      { costCentreID: ADMIN_CENTRE, amount: "18000.0000" },
    ]);
    expect(out.map((a) => a.posting_seq)).toEqual([0, 0]);
  });

  it("finds the expense leg when it is SECOND (the §1.2 offset case)", () => {
    const out = costSplitOn(expenseSecond, RENT, DEPARTMENT, [
      { costCentreID: SALES_CENTRE, amount: "27000.0000" },
      { costCentreID: ADMIN_CENTRE, amount: "18000.0000" },
    ]);
    expect(out.map((a) => a.posting_seq)).toEqual([1, 1]);
  });

  it("carries the axis and the centres through unchanged", () => {
    const out = costSplitOn(expenseSecond, RENT, DEPARTMENT, [
      { costCentreID: SALES_CENTRE, amount: "27000.0000" },
      { costCentreID: ADMIN_CENTRE, amount: "18000.0000" },
    ]);
    expect(out).toEqual([
      {
        posting_seq: 1,
        cost_category_id: DEPARTMENT,
        cost_centre_id: SALES_CENTRE,
        amount: "27000.0000",
      },
      {
        posting_seq: 1,
        cost_category_id: DEPARTMENT,
        cost_centre_id: ADMIN_CENTRE,
        amount: "18000.0000",
      },
    ]);
  });

  it("REFUSES a ledger that is on no posting", () => {
    expect(() =>
      costSplitOn(expenseFirst, "00000000-0000-0000-0000-000000000000", DEPARTMENT, [
        { costCentreID: SALES_CENTRE, amount: "45000.0000" },
      ]),
    ).toThrow(/no posting on this voucher is against ledger/);
  });

  // CONTROL for the refusal above: the same call with a ledger that IS on the
  // voucher succeeds, so the refusal is about the ledger and not about the
  // single-centre split or the amount.
  it("CONTROL: the same single-centre split against a real ledger is accepted", () => {
    expect(
      costSplitOn(expenseFirst, RENT, DEPARTMENT, [
        { costCentreID: SALES_CENTRE, amount: "45000.0000" },
      ]),
    ).toEqual([
      {
        posting_seq: 0,
        cost_category_id: DEPARTMENT,
        cost_centre_id: SALES_CENTRE,
        amount: "45000.0000",
      },
    ]);
  });

  it("REFUSES a ledger that appears on two postings — the seq would be a coin flip", () => {
    const twice: PostingRequest[] = [
      { ledger_id: RENT, dr_cr: "D", amount: "20000.0000" },
      { ledger_id: RENT, dr_cr: "D", amount: "25000.0000" },
      { ledger_id: BANK, dr_cr: "C", amount: "45000.0000" },
    ];
    expect(() =>
      costSplitOn(twice, RENT, DEPARTMENT, [
        { costCentreID: SALES_CENTRE, amount: "20000.0000" },
      ]),
    ).toThrow(/appears on 2 postings/);
  });
});

describe("costSplitOn enforces the per-(posting, CATEGORY) sum to the paisa", () => {
  // ⛔ ONE PAISA SHORT. The distinguishing input for "is there a tolerance?" —
  // a representative 27,000 / 18,000 split cannot answer that question, and a
  // float implementation of the same check would round this to equal.
  it("REFUSES a split one paisa short", () => {
    expect(() =>
      costSplitOn(expenseFirst, RENT, DEPARTMENT, [
        { costCentreID: SALES_CENTRE, amount: "27000.0000" },
        { costCentreID: ADMIN_CENTRE, amount: "17999.9999" },
      ]),
    ).toThrow(/totals 44999\.9999 but the posting is 45000\.0000 — 0\.0001 unattributed/);
  });

  it("REFUSES a split one paisa over", () => {
    expect(() =>
      costSplitOn(expenseFirst, RENT, DEPARTMENT, [
        { costCentreID: SALES_CENTRE, amount: "27000.0000" },
        { costCentreID: ADMIN_CENTRE, amount: "18000.0001" },
      ]),
    ).toThrow(/totals 45000\.0001 but the posting is 45000\.0000/);
  });

  // CONTROL: the same two centres, one paisa apart from the refused split,
  // are accepted — so the refusals above are about the TOTAL and not about the
  // centres, the axis or the posting.
  it("CONTROL: the exact split of the same posting across the same centres is accepted", () => {
    expect(
      costSplitOn(expenseFirst, RENT, DEPARTMENT, [
        { costCentreID: SALES_CENTRE, amount: "27000.0000" },
        { costCentreID: ADMIN_CENTRE, amount: "18000.0000" },
      ]),
    ).toHaveLength(2);
  });

  // ⛔ A THIRD-SHARE SPLIT THAT DOES NOT DIVIDE. 45,000 / 3 is exact, so it
  // proves nothing; 10,000 across three centres is 3333.3333 x2 + 3333.3334,
  // which only totals under exact decimal arithmetic.
  it("accepts a non-terminating split that has been rounded to close EXACTLY", () => {
    const tenK: PostingRequest[] = [
      { ledger_id: RENT, dr_cr: "D", amount: "10000.0000" },
      { ledger_id: BANK, dr_cr: "C", amount: "10000.0000" },
    ];
    expect(
      costSplitOn(tenK, RENT, DEPARTMENT, [
        { costCentreID: SALES_CENTRE, amount: "3333.3333" },
        { costCentreID: ADMIN_CENTRE, amount: "3333.3333" },
        { costCentreID: BRANCH, amount: "3333.3334" },
      ]),
    ).toHaveLength(3);
  });

  it("REFUSES the same centre twice on one axis", () => {
    expect(() =>
      costSplitOn(expenseFirst, RENT, DEPARTMENT, [
        { costCentreID: SALES_CENTRE, amount: "22500.0000" },
        { costCentreID: SALES_CENTRE, amount: "22500.0000" },
      ]),
    ).toThrow(/appears twice on posting 0/);
  });

  // CONTROL: the same two amounts against two DIFFERENT centres are accepted,
  // so the duplicate refusal is about the centre and not about the amounts
  // being equal.
  it("CONTROL: the same two amounts across two different centres are accepted", () => {
    expect(
      costSplitOn(expenseFirst, RENT, DEPARTMENT, [
        { costCentreID: SALES_CENTRE, amount: "22500.0000" },
        { costCentreID: ADMIN_CENTRE, amount: "22500.0000" },
      ]),
    ).toHaveLength(2);
  });

  it("REFUSES a non-positive share", () => {
    expect(() =>
      costSplitOn(expenseFirst, RENT, DEPARTMENT, [
        { costCentreID: SALES_CENTRE, amount: "45000.0000" },
        { costCentreID: ADMIN_CENTRE, amount: "0.0000" },
      ]),
    ).toThrow(/must be more than zero/);
  });

  it("REFUSES an empty split", () => {
    expect(() => costSplitOn(expenseFirst, RENT, DEPARTMENT, [])).toThrow(
      /names no cost centre/,
    );
  });

  // ⛔ THE PARALLEL-AXES CASE (HANDOFF §8.1). The SAME posting is allocated in
  // full twice, on two axes, and BOTH are correct. A version of this helper
  // that totalled across axes — or that refused a second split of a posting it
  // had already seen — would make the demo unable to show the one thing the
  // cost model is most often got wrong.
  it("allows the SAME posting to be allocated in full on a SECOND axis", () => {
    const dept = costSplitOn(expenseFirst, RENT, DEPARTMENT, [
      { costCentreID: SALES_CENTRE, amount: "27000.0000" },
      { costCentreID: ADMIN_CENTRE, amount: "18000.0000" },
    ]);
    const branch = costSplitOn(expenseFirst, RENT, BRANCH, [
      { costCentreID: SALES_CENTRE, amount: "31500.0000" },
      { costCentreID: ADMIN_CENTRE, amount: "13500.0000" },
    ]);
    // Same posting, two axes, each complete. Nothing here sums them.
    expect(dept.every((a) => a.posting_seq === 0)).toBe(true);
    expect(branch.every((a) => a.posting_seq === 0)).toBe(true);
    expect(new Set([...dept, ...branch].map((a) => a.cost_category_id)).size).toBe(2);
  });
});

describe("fulfilmentOn derives a 1-BASED line_no", () => {
  // A single-line voucher distinguishes 0-based from 1-based; a two-line one
  // where the target is SECOND additionally distinguishes "always 1" from
  // "index + 1". Both are needed.
  it("numbers a single-line voucher's only line 1, not 0", () => {
    expect(fulfilmentOn([line(ANGLE, "30.0000")], ANGLE, ORDER_LINE, "30.0000")).toEqual({
      line_no: 1,
      order_line_id: ORDER_LINE,
      qty: "30.0000",
    });
  });

  it("numbers the SECOND line 2 — not 1, and not its array index", () => {
    const inv = [line(SHEET, "25.0000"), line(ANGLE, "30.0000")];
    expect(fulfilmentOn(inv, ANGLE, ORDER_LINE, "30.0000").line_no).toBe(2);
  });

  it("REFUSES an item this voucher does not move", () => {
    expect(() =>
      fulfilmentOn([line(SHEET, "25.0000")], ANGLE, ORDER_LINE, "5.0000"),
    ).toThrow(/moves no /);
  });

  // CONTROL: the identical call for an item the voucher DOES move succeeds, so
  // the refusal is about the item and not about the quantity or the order line.
  it("CONTROL: the same call for an item the voucher does move is accepted", () => {
    expect(
      fulfilmentOn([line(SHEET, "25.0000")], SHEET, ORDER_LINE, "5.0000").qty,
    ).toBe("5.0000");
  });

  it("REFUSES an item that moves on two lines — which one discharged the order is a decision", () => {
    const inv = [line(ANGLE, "10.0000"), line(ANGLE, "20.0000")];
    expect(() => fulfilmentOn(inv, ANGLE, ORDER_LINE, "10.0000")).toThrow(
      /moves on 2 lines .*1-based 1, 2/,
    );
  });
});

describe("fulfilmentOn: a LINK is not a MOVEMENT", () => {
  // ⛔ PARTIAL IS THE NORMAL CASE and must be accepted without comment: the
  // whole feature is the outstanding balance.
  it("accepts a link SMALLER than the movement", () => {
    expect(
      fulfilmentOn([line(ANGLE, "30.0000")], ANGLE, ORDER_LINE, "12.0000").qty,
    ).toBe("12.0000");
  });

  it("accepts a link EXACTLY equal to the movement", () => {
    expect(
      fulfilmentOn([line(ANGLE, "30.0000")], ANGLE, ORDER_LINE, "30.0000").qty,
    ).toBe("30.0000");
  });

  // ⛔ ONE TEN-THOUSANDTH OVER. The distinguishing input for "is the comparison
  // <= or <, and is it exact?" — 31 against 30 would pass under a float
  // comparison too.
  it("REFUSES a link one ten-thousandth larger than the movement", () => {
    expect(() =>
      fulfilmentOn([line(ANGLE, "30.0000")], ANGLE, ORDER_LINE, "30.0001"),
    ).toThrow(/moved 30\.0000 but the link claims 30\.0001/);
  });

  it("REFUSES a zero or negative link", () => {
    expect(() =>
      fulfilmentOn([line(ANGLE, "30.0000")], ANGLE, ORDER_LINE, "0.0000"),
    ).toThrow(/must be more than zero/);
    expect(() =>
      fulfilmentOn([line(ANGLE, "30.0000")], ANGLE, ORDER_LINE, "-5.0000"),
    ).toThrow(/must be more than zero/);
  });
});
