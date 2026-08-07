import { describe, it, expect } from "vitest";
import {
  doubleEntryLinesFromView,
  doubleEntryRefusal,
  doubleEntryTotals,
  emptyDoubleEntryLine,
  keyedLines,
  type DoubleEntryLine,
} from "./doubleEntry";
import { effectiveEntryMode, entryModesFor } from "./voucherConfig";

/**
 * THE PURE HALF OF THE DOUBLE-ENTRY GRID.
 *
 * The shell-driving tests are in VoucherScreen.doubleEntry.test.tsx; this file
 * pins the arithmetic and every refusal, because those are what decide whether
 * a voucher is SENT and the server's balance constraint has no tolerance.
 */

function line(
  ledger: string,
  side: "DR" | "CR",
  amount: string,
): DoubleEntryLine {
  return { ...emptyDoubleEntryLine(), ledger, side, amount };
}

describe("the hole this module closes was FOUR classes, not one", () => {
  /**
   * ⛔ HANDOFF §4.5 named only the Journal, and the Journal is only the class
   * that DEFAULTS to double entry. `effectiveEntryMode` honours a Ctrl+H choice
   * of "double" on Payment / Receipt / Contra too — and that branch rendered
   * `null` just as F7 did, so those three reached the identical dead end one
   * keystroke away.
   *
   * This test states the reachability claim, which is the thing that decides
   * how much of the product was broken. It is a claim about voucherConfig, so
   * it lives with the module whose existence it justifies.
   */
  it("Payment / Receipt / Contra can be put into double entry by Ctrl+H", () => {
    for (const cls of ["payment", "receipt", "contra"] as const) {
      expect(entryModesFor(cls).map((o) => o.mode)).toContain("double");
      expect(effectiveEntryMode(cls, "double")).toBe("double");
    }
  });

  it("Journal is double entry with no choice offered — as in Tally", () => {
    expect(entryModesFor("journal").map((o) => o.mode)).toEqual(["double"]);
    // ⚠️ CLAMPED: even an explicit "single" cannot put a Journal into a form
    // that infers a leg. There is no single side to infer when both sides are
    // the point.
    expect(effectiveEntryMode("journal", "single")).toBe("double");
    expect(effectiveEntryMode("journal", undefined)).toBe("double");
  });

  it("a GST class is NOT diverted into the grid by a stale Ctrl+H choice", () => {
    // The control. `cfg.showGst` wins over the grid in both the render chain
    // and the accept dispatch; without this, a "double" remembered from a
    // Journal could route a Sales invoice through a builder that sends no GST
    // block and derives no tax.
    expect(effectiveEntryMode("sales", "double")).toBe("accounting-invoice");
    expect(effectiveEntryMode("purchase", "double")).toBe("accounting-invoice");
  });
});

describe("keyedLines decides what is SENT", () => {
  it("drops the blank trailing row the grid always offers", () => {
    expect(
      keyedLines([line("Rent", "DR", "100"), emptyDoubleEntryLine()]),
    ).toHaveLength(1);
  });

  it("drops a row with a ledger but no amount — a row in progress", () => {
    // The server refuses a zero-amount posting outright, so sending one costs
    // the operator the whole form for a line they had not finished typing.
    expect(keyedLines([line("Rent", "DR", "")])).toHaveLength(0);
    expect(keyedLines([line("Rent", "DR", "0")])).toHaveLength(0);
  });

  it("drops an amount with no ledger — there is nothing to post it to", () => {
    expect(keyedLines([line("", "DR", "500")])).toHaveLength(0);
    expect(keyedLines([line("   ", "DR", "500")])).toHaveLength(0);
  });
});

describe("doubleEntryTotals is exact to the paisa", () => {
  it("totals each side independently", () => {
    const t = doubleEntryTotals([
      line("Depreciation", "DR", "1250.5000"),
      line("Accumulated Depreciation", "CR", "1250.5000"),
    ]);
    expect(t.debit).toBe("1250.5000");
    expect(t.credit).toBe("1250.5000");
    expect(t.difference).toBe("0.0000");
    expect(t.balanced).toBe(true);
  });

  it("adds tenths without a float artefact — 0.1 + 0.2 === 0.3", () => {
    const t = doubleEntryTotals([
      line("A", "DR", "0.1000"),
      line("B", "DR", "0.2000"),
      line("C", "CR", "0.3000"),
    ]);
    expect(t.debit).toBe("0.3000");
    expect(t.difference).toBe("0.0000");
    expect(t.balanced).toBe(true);
  });

  /**
   * ⛔ THE INPUT THAT DISTINGUISHES THE TWO MONEY IMPLEMENTATIONS, and the
   * reason this module imports `parseFixed4` from itemInvoice.ts rather than
   * from money.ts.
   *
   * ⚠️ AND A CORRECTION TO THIS TEST'S OWN FIRST DRAFT, which asserted that
   * money.ts would get `0.1 + 0.2` wrong. IT DOES NOT. money.ts computes
   * `Math.round(Number.parseFloat(v) * 10000)`, and that rounding step absorbs
   * every float artefact up to roughly 2^53 / 10^4 — about ₹90,000 crore per
   * line. Measured, not assumed: the two implementations agree exactly on
   * 0.1000, 0.2000, 0.3000, 0.0001 and 999999999.9999.
   *
   * They diverge from ~10^12 upward, where the scaled value passes 2^53 and the
   * double can no longer hold the paisa:
   *
   *     "1234567890123.4567"  ->  money.ts 12345678901234568
   *                               exact    12345678901234567   (one paisa out)
   *
   * So the honest reason for the exact helpers is NOT that the float version is
   * wrong at ordinary amounts — it is that it is wrong PAST A THRESHOLD NOBODY
   * WILL REMEMBER, and a balance check has no tolerance to absorb it. The
   * server compares to the paisa; a form that disagrees with the server refuses
   * a postable voucher and gives the operator no way to make the difference go
   * away. costAllocation.ts made this same call, for this same reason, and says
   * so at its import.
   *
   * Swap this module's import to money.ts's arithmetic and THIS test goes red
   * while every other test in this file stays green — which is what makes it a
   * distinguishing case rather than a representative one.
   */
  it("keeps a paisa at a magnitude where float64 loses one", () => {
    const t = doubleEntryTotals([
      line("A", "DR", "1234567890123.4567"),
      line("B", "CR", "1234567890123.4566"),
    ]);
    expect(t.difference).toBe("0.0001");
    expect(t.balanced).toBe(false);
  });

  it("carries an ordinary large figure without losing a paisa", () => {
    const t = doubleEntryTotals([
      line("A", "DR", "999999999.9999"),
      line("B", "CR", "999999999.9998"),
    ]);
    expect(t.difference).toBe("0.0001");
    expect(t.balanced).toBe(false);
  });

  /**
   * ⛔ THE DEGENERATE CASE THAT SHIPS. An empty grid has debits equal to
   * credits — both zero — so a naive `dr === cr` calls it balanced and an empty
   * form posts a voucher with no postings.
   */
  it("an EMPTY grid is not 'balanced'", () => {
    expect(doubleEntryTotals([emptyDoubleEntryLine()]).balanced).toBe(false);
    expect(doubleEntryTotals([]).balanced).toBe(false);
  });
});

describe("doubleEntryRefusal names both figures and the difference", () => {
  it("accepts a balanced two-line entry", () => {
    expect(
      doubleEntryRefusal([
        line("Depreciation", "DR", "1250"),
        line("Accumulated Depreciation", "CR", "1250"),
      ]),
    ).toBe("");
  });

  it("refuses an unbalanced grid, quoting BOTH totals and the gap", () => {
    const r = doubleEntryRefusal([
      line("Rent", "DR", "5000"),
      line("Bank", "CR", "3000"),
    ]);
    // Not merely "does not balance": the operator must not have to do the
    // arithmetic the machine already did.
    expect(r).toContain("5000.0000");
    expect(r).toContain("3000.0000");
    expect(r).toContain("2000.0000");
    expect(r).toContain("Debits exceed credits");
  });

  it("names the direction correctly when CREDITS are the larger side", () => {
    // The control for the message above. A refusal that always said "debits
    // exceed credits" would read as coverage and point the operator at the
    // wrong side of their own voucher.
    const r = doubleEntryRefusal([
      line("Rent", "DR", "3000"),
      line("Bank", "CR", "5000"),
    ]);
    expect(r).toContain("Credits exceed debits");
    expect(r).toContain("2000.0000");
  });

  it("refuses a one-sided voucher by naming the side that is MISSING", () => {
    const r = doubleEntryRefusal([
      line("Rent", "DR", "3000"),
      line("Salaries", "DR", "2000"),
    ]);
    expect(r).toContain("same side");
    expect(r).toContain("CREDIT");
    // ⚠️ And it must NOT fall through to the generic imbalance message, which
    // would tell an operator their credits total 0.0000 — true, unhelpful, and
    // it hides the actual mistake (every row is on one side).
    expect(r).not.toContain("exceed");
  });

  it("refuses a single line — it cannot balance against anything", () => {
    expect(doubleEntryRefusal([line("Rent", "DR", "3000")])).toContain(
      "at least two lines",
    );
  });

  it("refuses an empty grid without pretending it is unbalanced", () => {
    expect(doubleEntryRefusal([emptyDoubleEntryLine()])).toContain(
      "Nothing has been keyed",
    );
  });

  /**
   * ⛔ A NEGATIVE AMOUNT IS REFUSED, NOT REINTERPRETED. A -500 debit that
   * silently became a 500 credit would balance, would post, and would make the
   * Dr/Cr selector a lie about what is in the ledger. Tally's own answer to
   * "this belongs on the other side" is to key it on the other side.
   */
  it("refuses a negative amount rather than flipping its side", () => {
    const r = doubleEntryRefusal([
      line("Rent", "DR", "-500"),
      line("Bank", "CR", "-500"),
    ]);
    expect(r).toContain("NEGATIVE");
    expect(r).toContain("Rent");
  });
});

describe("doubleEntryLinesFromView — the hydration an ALTER depends on", () => {
  const postings = [
    { ledger: "Depreciation", ledger_id: "L-dep", dr_cr: "D", amount: "1250.0000" },
    {
      ledger: "Accumulated Depreciation",
      ledger_id: "L-acc",
      dr_cr: "C",
      amount: "1250.0000",
    },
  ];

  it("takes BOTH sides, in the response's order, with sides preserved", () => {
    // ⚠️ Unlike `linesFromPostings`, which keeps only the many side because
    // single entry synthesises the other. Here there is nothing to synthesise,
    // and a hydration that dropped a side would post a one-sided voucher.
    const rows = doubleEntryLinesFromView(postings, () => [], () => []);
    expect(rows).toHaveLength(2);
    expect(rows[0]!.side).toBe("DR");
    expect(rows[1]!.side).toBe("CR");
    expect(rows.map((r) => r.ledger)).toEqual([
      "Depreciation",
      "Accumulated Depreciation",
    ]);
    expect(doubleEntryTotals(rows).balanced).toBe(true);
  });

  it("matches cost splits on the FULL response index, not a filtered one", () => {
    const rows = doubleEntryLinesFromView(
      postings,
      (seq) =>
        seq === 1
          ? [
              {
                costCategoryId: "cat",
                costCentreId: "ctr",
                amount: "1250.0000",
                categoryName: "Department",
                centrePath: "Admin",
              },
            ]
          : [],
      () => [],
    );
    expect(rows[0]!.allocations).toHaveLength(0);
    expect(rows[1]!.allocations).toHaveLength(1);
  });

  /**
   * ⚠️ A ledger posted on TWO lines would otherwise be given its bill
   * references twice — once per row — and the rebuilt voucher would claim to
   * settle double what it settles. The server's sum-to-posting invariant then
   * refuses the alter, with the operator's whole form as the cost.
   */
  it("gives a repeated ledger its bill references exactly ONCE", () => {
    const twice = [
      { ledger: "Acme", ledger_id: "L-acme", dr_cr: "D", amount: "100.0000" },
      { ledger: "Acme", ledger_id: "L-acme", dr_cr: "D", amount: "200.0000" },
      { ledger: "Sales", ledger_id: "L-sales", dr_cr: "C", amount: "300.0000" },
    ];
    const rows = doubleEntryLinesFromView(
      twice,
      () => [],
      (id) =>
        id === "L-acme"
          ? [
              {
                refType: "AGAINST" as const,
                refName: "7",
                amount: "300.0000",
                dueDate: "",
                auto: false,
              },
            ]
          : [],
    );
    expect(rows[0]!.billRefs).toHaveLength(1);
    expect(rows[1]!.billRefs).toHaveLength(0);
    expect(rows[2]!.billRefs).toHaveLength(0);
  });

  it("yields one blank row for a voucher with no postings", () => {
    expect(doubleEntryLinesFromView([], () => [], () => [])).toHaveLength(1);
  });
});
