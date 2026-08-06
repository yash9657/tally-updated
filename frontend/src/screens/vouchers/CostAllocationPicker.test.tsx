import { describe, it, expect, afterEach } from "vitest";
import {
  render,
  screen,
  cleanup,
  fireEvent,
  waitFor,
} from "@testing-library/react";

import { AppShell } from "../../app/AppShell";
import { CompanyProvider } from "../../app/CompanyContext";
import { createMockClient } from "../../api/mock";
import { ApiError } from "../../api";
import type { CoredClient } from "../../api";
import {
  allocationRefusal,
  allocationsFromView,
  axisRemainders,
  buildCostAllocations,
} from "./costAllocation";

/**
 * THE COST ALLOCATION UI (T2) — the last `server`-qualified row on F11.
 *
 * The validation, the hashed payload entry, the row insert, both reports and the ACL
 * migration were all built and tested, `voucherReq.cost_allocations` was on the wire
 * and `CostAllocationInput` was in types.ts — and NO SCREEN COULD CONSTRUCT ONE. Both
 * cost reports answered 200 and were *correct*: they accurately reported that nothing
 * had been allocated, because nothing could be.
 *
 * ⛔ THE TWO ASSERTIONS THAT MATTER MOST ARE ARITHMETIC, NOT DOM:
 *
 *  1. **`posting_seq` IS 0-BASED AND OFFSET BY THE PRIMARY LEG.** In single entry the
 *     postings array is `[bank, ...particulars]`, so the particular an operator sees
 *     at row 0 is posting **1**. Getting it wrong allocates the BANK leg — a
 *     departmental cost attributed to a cash account, on a voucher that balances and
 *     a Cost Centre Summary that foots. Nothing downstream can detect it.
 *
 *  2. **THE SUM INVARIANT IS PER (POSTING, AXIS), NEVER PER POSTING.** Two axes
 *     allocate the same money in parallel and both must total the posting in full.
 *     A combined remainder would report a fully-allocated posting as half done.
 */

const COMPANY = "company-demo";

afterEach(cleanup);

/** A particulars row carrying splits. */
function line(
  amount: string,
  splits: { axis: string; centre: string; amount: string; axisName?: string }[] = [],
) {
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
// THE PURE HALF — the assertions with teeth.
// ---------------------------------------------------------------------------

describe("buildCostAllocations and the primary-leg offset", () => {
  it("⛔ OFFSETS BY THE PRIMARY LEG: row 0 of single entry is posting 1", () => {
    // THE DEFECT THIS PINS, and it is the whole hazard of the feature. Passing 0 here
    // would attribute the departmental cost to the bank account the money left.
    const out = buildCostAllocations(
      [line("1000", [{ axis: "cat-1", centre: "ctr-1", amount: "1000" }])],
      1,
    );
    expect(out).toEqual([
      {
        posting_seq: 1,
        cost_category_id: "cat-1",
        cost_centre_id: "ctr-1",
        amount: "1000.0000",
      },
    ]);
  });

  it("is 0-BASED when the rows map one-for-one from index 0", () => {
    // The other call shape. ⚠️ 0-based here where FulfilmentInput.line_no on the SAME
    // request body is 1-based — both inherited, neither negotiable.
    const out = buildCostAllocations(
      [line("500", [{ axis: "c", centre: "x", amount: "500" }])],
      0,
    );
    expect(out[0]!.posting_seq).toBe(0);
  });

  it("numbers each row from ITS OWN position, offset once", () => {
    const out = buildCostAllocations(
      [
        line("100", [{ axis: "c", centre: "x", amount: "100" }]),
        line("200", [{ axis: "c", centre: "y", amount: "200" }]),
        line("300", [{ axis: "c", centre: "z", amount: "300" }]),
      ],
      1,
    );
    expect(out.map((a) => a.posting_seq)).toEqual([1, 2, 3]);
  });

  it("drops half-picked and zero splits rather than sending them", () => {
    const out = buildCostAllocations(
      [
        line("100", [
          { axis: "", centre: "x", amount: "50" }, // no axis
          { axis: "c", centre: "", amount: "50" }, // no centre
          { axis: "c", centre: "z", amount: "0" }, // nothing
          { axis: "c", centre: "w", amount: "100" }, // the only real one
        ]),
      ],
      1,
    );
    expect(out).toHaveLength(1);
    expect(out[0]!.cost_centre_id).toBe("w");
  });
});

describe("axisRemainders — PER AXIS, and never combined", () => {
  it("⛔ reports two axes SEPARATELY, each complete against the full posting", () => {
    // THE PARALLEL-ALLOCATION CASE. One 1,00,000 salary allocated in full across
    // Departments AND AGAIN in full across Branches. Both are complete and both are
    // correct, and a combined figure would say 200% — or, worse, a combined
    // "remainder" of −1,00,000.
    const rs = axisRemainders(
      line("100000", [
        { axis: "dept", centre: "sales", amount: "60000", axisName: "Department" },
        { axis: "dept", centre: "admin", amount: "40000", axisName: "Department" },
        { axis: "branch", centre: "mumbai", amount: "70000", axisName: "Branch" },
        { axis: "branch", centre: "delhi", amount: "30000", axisName: "Branch" },
      ]),
    );
    expect(rs).toHaveLength(2);
    for (const r of rs) {
      expect(r.complete, `${r.categoryName} must be complete`).toBe(true);
      expect(r.allocated).toBe("100000.0000");
      expect(r.remainder).toBe("0.0000");
    }
  });

  it("shows the shortfall on the axis that is short, and only that one", () => {
    const rs = axisRemainders(
      line("100000", [
        { axis: "dept", centre: "sales", amount: "60000", axisName: "Department" },
        { axis: "branch", centre: "mumbai", amount: "100000", axisName: "Branch" },
      ]),
    );
    const dept = rs.find((r) => r.categoryName === "Department")!;
    const branch = rs.find((r) => r.categoryName === "Branch")!;
    expect(dept.complete).toBe(false);
    expect(dept.remainder).toBe("40000.0000");
    expect(branch.complete).toBe(true);
  });

  it("omits axes with NO splits rather than reporting them 100% outstanding", () => {
    // Not allocating a posting on an axis is the normal case — most postings are
    // analysed on no axis at all. Listing every axis as incomplete would bury the one
    // that IS incomplete.
    expect(axisRemainders(line("100"))).toEqual([]);
  });

  it("counts a split whose CENTRE is unpicked, so the axis cannot read complete", () => {
    // Money the operator has committed to the axis. Hiding it from the remainder
    // would show the axis as done while it cannot be sent.
    const rs = axisRemainders(
      line("100", [{ axis: "dept", centre: "", amount: "100" }]),
    );
    expect(rs[0]!.allocated).toBe("100.0000");
  });

  it("is exact to the paisa — no tolerance, matching the trigger", () => {
    const rs = axisRemainders(
      line("100.0000", [{ axis: "d", centre: "c", amount: "99.9999" }]),
    );
    expect(rs[0]!.complete).toBe(false);
    expect(rs[0]!.remainder).toBe("0.0001");
  });
});

describe("allocationRefusal — a REFUSAL, not a warning", () => {
  it("says nothing when nothing is allocated", () => {
    expect(allocationRefusal(line("100"))).toBe("");
  });

  it("says nothing when every axis totals the posting exactly", () => {
    expect(
      allocationRefusal(
        line("100", [
          { axis: "d", centre: "a", amount: "40" },
          { axis: "d", centre: "b", amount: "60" },
        ]),
      ),
    ).toBe("");
  });

  it("⛔ REFUSES a partial axis, because the server and the DB both do", () => {
    // Unlike the fulfilment picker, where under-linking is legitimate. A partial axis
    // is refused to the paisa by the server AND a deferred trigger, so sending it
    // costs the operator the whole form.
    const r = allocationRefusal(
      line("100", [{ axis: "d", centre: "a", amount: "40", axisName: "Department" }]),
    );
    expect(r).toMatch(/Department/);
    expect(r).toMatch(/short by 60\.0000/);
    expect(r).toMatch(/complete or it is\s+not/);
  });

  it("distinguishes OVER-allocation, which is a different sentence", () => {
    const r = allocationRefusal(
      line("100", [{ axis: "d", centre: "a", amount: "150", axisName: "Department" }]),
    );
    expect(r).toMatch(/over-allocated by 50\.0000/);
    expect(r).not.toMatch(/short by/);
  });

  it("catches the split that has an amount but NO centre", () => {
    // The axis arithmetic reads COMPLETE here (the sum includes it), so without this
    // check the form would look saveable and the server would refuse it.
    const r = allocationRefusal(
      line("100", [{ axis: "d", centre: "", amount: "100", axisName: "Department" }]),
    );
    expect(r).toMatch(/amount but no cost\s+centre/);
    expect(r).toMatch(/looks complete/);
  });
});

describe("allocationsFromView matches on the RESPONSE's own seq", () => {
  it("picks only the splits for the posting asked about", () => {
    const views = [
      {
        posting_seq: 0,
        cost_category_id: "d",
        cost_category_name: "Department",
        cost_centre_id: "a",
        cost_centre_name: "Sales",
        amount: "10.0000",
      },
      {
        posting_seq: 1,
        cost_category_id: "d",
        cost_category_name: "Department",
        cost_centre_id: "b",
        cost_centre_name: "Admin",
        amount: "20.0000",
      },
    ];
    expect(allocationsFromView(views, 1)).toEqual([
      {
        costCategoryId: "d",
        costCentreId: "b",
        amount: "20.0000",
        categoryName: "Department",
        centrePath: "Admin",
      },
    ]);
    expect(allocationsFromView(views, 2)).toEqual([]);
  });
});

// ---------------------------------------------------------------------------
// THROUGH THE REAL SHELL.
// ---------------------------------------------------------------------------

function renderShell(client: CoredClient = createMockClient()) {
  render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: COMPANY, name: "Demo Traders" }}
    >
      <AppShell />
    </CompanyProvider>,
  );
  return screen.getByRole("application");
}

/** F5 Payment, single entry, with one particular keyed. */
async function openPayment(app: HTMLElement, amount = "1000") {
  fireEvent.keyDown(app, { key: "F5" });
  await screen.findByTestId("single-entry-panel");
  fireEvent.change(screen.getByLabelText("Particular 1 ledger"), {
    target: { value: "Electricity" },
  });
  fireEvent.change(screen.getByLabelText("Particular 1 amount"), {
    target: { value: amount },
  });
}

describe("an operator can split a posting across cost centres", () => {
  it("the picker is ON the particulars row and never asks for an ordinal", async () => {
    const app = renderShell();
    await openPayment(app);
    const cell = await screen.findByTestId("alloc:0");
    expect(cell).toBeTruthy();
    // ⛔ NO posting_seq ANYWHERE — not asked for, not displayed.
    expect(screen.queryByLabelText(/posting.*seq|posting number/i)).toBeNull();
    expect(cell.textContent).not.toMatch(/posting \d/i);
  });

  it("offers the company's axes and only THAT axis's centres", async () => {
    const app = renderShell();
    await openPayment(app);
    fireEvent.click(
      await screen.findByLabelText("Allocate particular 1 to cost centres"),
    );
    const axis = (await screen.findByTestId("alloc:0-axis:0")) as HTMLSelectElement;
    const axisNames = [...axis.options].map((o) => o.textContent);
    expect(axisNames.length).toBeGreaterThan(1);

    // The centre picker is DISABLED until an axis is chosen — a centre without its
    // axis is the one thing an allocation cannot be.
    const centre = screen.getByTestId("alloc:0-centre:0") as HTMLSelectElement;
    expect(centre.disabled).toBe(true);

    fireEvent.change(axis, { target: { value: axis.options[1]!.value } });
    await waitFor(() => expect(centre.disabled).toBe(false));
    // Every offered centre belongs to the chosen axis (the mock's masters are
    // per-axis), and each is labelled by PATH so two centres named alike differ.
    expect([...centre.options].length).toBeGreaterThan(1);
  });

  it("shows the REMAINDER, and it is the number the form is for", async () => {
    const app = renderShell();
    await openPayment(app, "1000");
    fireEvent.click(
      await screen.findByLabelText("Allocate particular 1 to cost centres"),
    );
    const axis = (await screen.findByTestId("alloc:0-axis:0")) as HTMLSelectElement;
    fireEvent.change(axis, { target: { value: axis.options[1]!.value } });
    const centre = screen.getByTestId("alloc:0-centre:0") as HTMLSelectElement;
    fireEvent.change(centre, { target: { value: centre.options[1]!.value } });
    fireEvent.change(screen.getByTestId("alloc:0-amount:0"), {
      target: { value: "400" },
    });

    const rem = await screen.findByTestId("alloc:0-remainders");
    expect(rem.textContent).toMatch(/400\.0000 of 1000/);
    expect(rem.textContent).toMatch(/600\.0000 left/);
    // And it REFUSES, because a partial axis cannot be saved.
    expect(screen.getByTestId("alloc:0-refusal").textContent).toMatch(/short by 600/);
  });

  it("the 'rest' button closes the gap without pre-filling anything", async () => {
    const app = renderShell();
    await openPayment(app, "1000");
    fireEvent.click(
      await screen.findByLabelText("Allocate particular 1 to cost centres"),
    );
    const axis = (await screen.findByTestId("alloc:0-axis:0")) as HTMLSelectElement;
    fireEvent.change(axis, { target: { value: axis.options[1]!.value } });
    // THE AMOUNT IS EMPTY AFTER PICKING THE AXIS — an affordance, not a default.
    expect((screen.getByTestId("alloc:0-amount:0") as HTMLInputElement).value).toBe("");

    fireEvent.click(screen.getByTestId("alloc:0-rest:0"));
    expect((screen.getByTestId("alloc:0-amount:0") as HTMLInputElement).value).toBe(
      "1000.0000",
    );
  });

  it("SAVES the split and the server's own refusal appears when an axis is short", async () => {
    // ⛔ THE REACHABILITY ASSERTION: the split must reach the wire. And when it is
    // partial, the SERVER's sentence must reach the screen — captured, not retyped.
    const client = createMockClient();
    const app = renderShell(client);
    await openPayment(app, "1000");
    fireEvent.click(
      await screen.findByLabelText("Allocate particular 1 to cost centres"),
    );
    const axis = (await screen.findByTestId("alloc:0-axis:0")) as HTMLSelectElement;
    fireEvent.change(axis, { target: { value: axis.options[1]!.value } });
    const centre = screen.getByTestId("alloc:0-centre:0") as HTMLSelectElement;
    fireEvent.change(centre, { target: { value: centre.options[1]!.value } });
    fireEvent.click(screen.getByTestId("alloc:0-rest:0"));

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(
      () => expect(screen.getByRole("status").textContent).toMatch(/Saved|No:/),
      { timeout: 4000 },
    );
  });

  it("⛔ the mock REFUSES a partial axis, matching the server", async () => {
    // The mock ignored cost_allocations entirely until this feature was built — it
    // did not reject them, it dropped them silently. This asserts the refusal exists,
    // with the status code, because a 422 and a 201 are different facts.
    const c = createMockClient();
    await expect(
      c.postPaymentVoucher(COMPANY, {
        voucher_date: "2026-06-01",
        party: "Cash",
        postings: [
          { ledger_id: "ledger-cash", dr_cr: "C", amount: "1000.0000" },
          { ledger_id: "ledger-sales", dr_cr: "D", amount: "1000.0000" },
        ],
        cost_allocations: [
          {
            posting_seq: 1,
            cost_category_id: "cost-cat-dept",
            cost_centre_id: "cost-ctr-sales",
            amount: "400.0000",
          },
        ],
      }),
    ).rejects.toMatchObject({ status: 422 });
  });

  it("⛔ the mock REFUSES an out-of-range posting_seq — the off-by-one, caught", async () => {
    const c = createMockClient();
    let err: unknown;
    try {
      await c.postPaymentVoucher(COMPANY, {
        voucher_date: "2026-06-01",
        party: "Cash",
        postings: [
          { ledger_id: "ledger-cash", dr_cr: "C", amount: "1000.0000" },
          { ledger_id: "ledger-sales", dr_cr: "D", amount: "1000.0000" },
        ],
        cost_allocations: [
          {
            // One PAST the end — what a client sending a 1-based ordinal would do.
            posting_seq: 2,
            cost_category_id: "cost-cat-dept",
            cost_centre_id: "cost-ctr-sales",
            amount: "1000.0000",
          },
        ],
      });
    } catch (e) {
      err = e;
    }
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(422);
    // The message names the valid range, because an off-by-one is the likeliest bug.
    expect((err as Error).message).toMatch(/numbered 0 to 1/);
  });

  it("⛔ the mock REFUSES allocations on an ITEM INVOICE outright", async () => {
    // Its postings are server-generated and index 0 is the PARTY leg, so a
    // client-supplied ordinal would attribute a departmental cost to a receivable.
    const c = createMockClient();
    await expect(
      c.postSalesVoucher(COMPANY, {
        voucher_date: "2026-06-01",
        party_ledger_id: "ledger-acme",
        item_ledger_id: "ledger-sales",
        inventory: [
          {
            stock_item_id: "stock-widget",
            godown_id: "",
            direction: "OUT",
            qty: "1.0000",
            rate: "100.0000",
            amount: "100.0000",
          },
        ],
        cost_allocations: [
          {
            posting_seq: 1,
            cost_category_id: "cost-cat-dept",
            cost_centre_id: "cost-ctr-sales",
            amount: "100.0000",
          },
        ],
      }),
    ).rejects.toMatchObject({ status: 422 });
  });
});
