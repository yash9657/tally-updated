import { describe, it, expect, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";

import { AppShell } from "../../app/AppShell";
import { CompanyProvider } from "../../app/CompanyContext";
import { createMockClient } from "../../api/mock";
// The EXACT BigInt-scaled parser the module itself uses, so the oracle cannot
// disagree with the code about what "1000" is worth to the paisa.
import { parseFixed4 } from "./itemInvoice";
import {
  allocationRefusal,
  axisRemainders,
  buildCostAllocations,
  type AllocatableLine,
} from "./costAllocation";

/**
 * ⛔⛔ THE SCREEN AND THE WIRE MUST AGREE ABOUT WHAT HAS BEEN ALLOCATED (D67).
 *
 * The picker reported an axis "allocated in full" when one of its splits carried an
 * amount and NO cost centre: `axisRemainders` added that split into the axis total,
 * `buildCostAllocations` DROPPED it, and Ctrl+A sent a SHORT axis — refused by the
 * server with a 422 (and by a deferred database trigger behind it) that costs the
 * operator the whole form. The screen was the flattering one, which is this repo's
 * signature failure: a total that adds up while the parts are wrong.
 *
 * ⛔ SO NOTHING HERE ASSERTS A TOTAL ON ITS OWN. Every split assertion names the
 * AXIS, the CENTRE and that centre's own amount, because a reversed or dropped
 * share leaves the total untouched — 600 + 400 and 1000 + 0 are the same number and
 * completely different books.
 *
 * The load-bearing test is {@link the property}: for every axis on every line,
 *
 *     remainder === posting − Σ(what buildCostAllocations actually emits)
 *
 * A spot check on the one input the defect was found with would have passed in
 * round two; the property is what makes a NEW way of dropping a split fail here.
 */

const COMPANY = "company-demo";

afterEach(cleanup);

/** A particulars row carrying splits, in the shape the picker hands around. */
function line(
  amount: string,
  splits: { axis: string; centre: string; amount: string; axisName?: string }[] = [],
): AllocatableLine {
  return {
    amount,
    allocations: splits.map((s) => ({
      costCategoryId: s.axis,
      costCentreId: s.centre,
      amount: s.amount,
      categoryName: s.axisName ?? s.axis,
      centrePath: s.centre,
    })),
  };
}

// ---------------------------------------------------------------------------
// THE DEFECT, EXACTLY AS THE AUDIT FOUND IT.
// ---------------------------------------------------------------------------

describe("⛔ D67 — an axis with a centre-less split is not 'allocated in full'", () => {
  it("does not read complete, and the remainder shows what is really missing", () => {
    const l = line("1000", [
      { axis: "dept", centre: "sales", amount: "600", axisName: "Department" },
      // The operator typed the amount and never got to the centre picker.
      { axis: "dept", centre: "", amount: "400", axisName: "Department" },
    ]);
    const [dept] = axisRemainders(l);
    expect(dept!.categoryName).toBe("Department");
    // ⛔ 600, NOT 1000. Only the sendable split is allocated.
    expect(dept!.allocated).toBe("600.0000");
    expect(dept!.remainder).toBe("400.0000");
    expect(dept!.unassigned).toBe("400.0000");
    expect(
      dept!.complete,
      "the axis reported complete while 400 of it would never leave the browser",
    ).toBe(false);
  });

  it("⛔ the axis Ctrl+A would send is SHORT, and the split names its own centre", () => {
    const l = line("1000", [
      { axis: "dept", centre: "sales", amount: "600", axisName: "Department" },
      { axis: "dept", centre: "", amount: "400", axisName: "Department" },
    ]);
    // Single entry: postings are [bank, ...particulars], so row 0 is posting 1.
    const wire = buildCostAllocations([l], 1);
    // ⛔ ASSERT THE WHOLE ROW, not the count and not the sum: the axis, the centre,
    // the amount and the posting it hangs off. A dropped split and a mis-keyed one
    // are indistinguishable from a length check.
    expect(wire).toEqual([
      {
        posting_seq: 1,
        cost_category_id: "dept",
        cost_centre_id: "sales",
        amount: "600.0000",
      },
    ]);
    // And the refusal must fire BEFORE that goes anywhere.
    const r = allocationRefusal(l);
    expect(r).toMatch(/Department/);
    expect(r).toMatch(/400\.0000 against no cost centre/);
    expect(r).toMatch(/NOT SENT/);
  });

  it("⛔ THE OVER-KEYED CASE: the arithmetic alone cannot see this one", () => {
    // The sendable splits total the posting EXACTLY. The server would take it. And
    // the 400 the operator keyed into the third row would simply cease to exist,
    // with the form showing green and nothing anywhere saying a number was thrown
    // away. This is why `complete` tests `unassigned === 0`, not just the sum.
    const l = line("1000", [
      { axis: "dept", centre: "sales", amount: "600", axisName: "Department" },
      { axis: "dept", centre: "admin", amount: "400", axisName: "Department" },
      { axis: "dept", centre: "", amount: "400", axisName: "Department" },
    ]);
    const [dept] = axisRemainders(l);
    expect(dept!.allocated).toBe("1000.0000");
    expect(dept!.remainder).toBe("0.0000");
    expect(dept!.unassigned).toBe("400.0000");
    expect(dept!.complete).toBe(false);
    expect(allocationRefusal(l)).toMatch(/400\.0000 against no cost centre/);
    // ...and it must NOT be the useless sentence the shortfall branch would give:
    // "short by 0.0000" is arithmetically true and tells the operator nothing.
    expect(allocationRefusal(l)).not.toMatch(/short by 0\.0000/);
  });

  it("a centre-less split with NO amount is still not an error", () => {
    // An empty row the operator has opened and not filled in. It sends nothing and
    // it costs nothing; refusing it would refuse the act of starting to key.
    const l = line("1000", [
      { axis: "dept", centre: "sales", amount: "1000", axisName: "Department" },
      { axis: "dept", centre: "", amount: "", axisName: "Department" },
    ]);
    expect(axisRemainders(l)[0]!.complete).toBe(true);
    expect(allocationRefusal(l)).toBe("");
  });

  it("the PARALLEL-AXES case is untouched: one axis's gap is not the other's", () => {
    // ⛔ RULE: nothing may total across axes. Department is broken by an unassigned
    // split; Branch is complete and correct and must not be reported at all.
    const l = line("100000", [
      { axis: "dept", centre: "", amount: "60000", axisName: "Department" },
      { axis: "branch", centre: "mumbai", amount: "70000", axisName: "Branch" },
      { axis: "branch", centre: "delhi", amount: "30000", axisName: "Branch" },
    ]);
    const rs = axisRemainders(l);
    const branch = rs.find((r) => r.categoryName === "Branch")!;
    const dept = rs.find((r) => r.categoryName === "Department")!;
    expect(branch.complete).toBe(true);
    expect(branch.unassigned).toBe("0.0000");
    expect(dept.complete).toBe(false);
    expect(allocationRefusal(l)).toMatch(/Department/);
    expect(
      allocationRefusal(l),
      "the refusal named the Branch axis, which is complete — the operator would go and 'fix' a split that was right",
    ).not.toMatch(/Branch/);
    // The Branch shares reach the wire whole, each on its own centre.
    expect(buildCostAllocations([l], 0)).toEqual([
      {
        posting_seq: 0,
        cost_category_id: "branch",
        cost_centre_id: "mumbai",
        amount: "70000.0000",
      },
      {
        posting_seq: 0,
        cost_category_id: "branch",
        cost_centre_id: "delhi",
        amount: "30000.0000",
      },
    ]);
  });
});

// ---------------------------------------------------------------------------
// THE PROPERTY — the CLASS, not the call site.
// ---------------------------------------------------------------------------

describe("⛔ the remainder describes the WIRE, for every shape of line", () => {
  const cases: { name: string; line: AllocatableLine }[] = [
    { name: "nothing allocated", line: line("1000") },
    {
      name: "one complete axis",
      line: line("1000", [{ axis: "d", centre: "a", amount: "1000" }]),
    },
    {
      name: "a centre-less split (D67)",
      line: line("1000", [
        { axis: "d", centre: "a", amount: "600" },
        { axis: "d", centre: "", amount: "400" },
      ]),
    },
    {
      name: "a ZERO split, which the builder also drops",
      line: line("1000", [
        { axis: "d", centre: "a", amount: "1000" },
        { axis: "d", centre: "b", amount: "0" },
      ]),
    },
    {
      name: "an over-allocated axis",
      line: line("100", [
        { axis: "d", centre: "a", amount: "60" },
        { axis: "d", centre: "b", amount: "70" },
      ]),
    },
    {
      name: "two axes in parallel, one broken",
      line: line("100000", [
        { axis: "d", centre: "a", amount: "60000" },
        { axis: "d", centre: "", amount: "40000" },
        { axis: "b", centre: "m", amount: "100000" },
      ]),
    },
  ];

  for (const c of cases) {
    it(`${c.name}: remainder === posting − what is emitted, per axis`, () => {
      const emitted = buildCostAllocations([c.line], 0);
      for (const r of axisRemainders(c.line)) {
        const sentPaise = emitted
          .filter((e) => e.cost_category_id === r.costCategoryId)
          .reduce((n, e) => n + BigInt(e.amount.replace(".", "")), 0n);
        expect(
          r.allocated.replace(".", ""),
          `${r.costCategoryId}: the picker says ${r.allocated} is allocated but ${sentPaise} paise reach the request`,
        ).toBe(sentPaise.toString());
        // And "complete" is never true of an axis the request would not carry
        // WHOLE — the assertion the old code failed, because a centre-less split
        // made the screen's total right and the request's total wrong.
        if (r.complete) {
          expect(
            sentPaise,
            `${r.costCategoryId} reads complete but the request carries ${sentPaise} paise of a ${c.line.amount} posting`,
          ).toBe(parseFixed4(c.line.amount));
        }
      }
    });
  }
});

// ---------------------------------------------------------------------------
// THROUGH THE REAL SHELL — the words the operator actually reads.
// ---------------------------------------------------------------------------

describe("⛔ the picker no longer says 'allocated in full' over a dropped split", () => {
  it("shows the shortfall and the refusal instead", async () => {
    render(
      <CompanyProvider
        client={createMockClient()}
        initialCompany={{ id: COMPANY, name: "Demo Traders" }}
      >
        <AppShell />
      </CompanyProvider>,
    );
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F5" });
    await screen.findByTestId("single-entry-panel");
    fireEvent.change(screen.getByLabelText("Particular 1 ledger"), {
      target: { value: "Electricity" },
    });
    fireEvent.change(screen.getByLabelText("Particular 1 amount"), {
      target: { value: "1000" },
    });

    fireEvent.click(
      await screen.findByLabelText("Allocate particular 1 to cost centres"),
    );
    const axis = (await screen.findByTestId("alloc:0-axis:0")) as HTMLSelectElement;
    const axisValue = axis.options[1]!.value;
    fireEvent.change(axis, { target: { value: axisValue } });
    const centre = screen.getByTestId("alloc:0-centre:0") as HTMLSelectElement;
    await waitFor(() => expect(centre.disabled).toBe(false));
    fireEvent.change(centre, { target: { value: centre.options[1]!.value } });
    fireEvent.change(screen.getByTestId("alloc:0-amount:0"), {
      target: { value: "600" },
    });

    // A second split on the SAME axis, with an amount and no centre — the D67 shape.
    fireEvent.click(screen.getByTestId("alloc:0-more"));
    const axis2 = (await screen.findByTestId("alloc:0-axis:1")) as HTMLSelectElement;
    fireEvent.change(axis2, { target: { value: axisValue } });
    fireEvent.change(screen.getByTestId("alloc:0-amount:1"), {
      target: { value: "400" },
    });

    const rem = await screen.findByTestId("alloc:0-remainders");
    expect(
      rem.textContent,
      "the axis still reads as fully allocated while 400 of it would be dropped",
    ).not.toMatch(/allocated in full/);
    expect(rem.textContent).toMatch(/600\.0000 of 1000/);
    expect(rem.textContent).toMatch(/400\.0000 left/);
    expect(screen.getByTestId("alloc:0-refusal").textContent).toMatch(/NOT SENT/);
  });
});
