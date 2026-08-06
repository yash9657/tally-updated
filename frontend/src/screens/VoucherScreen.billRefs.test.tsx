import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";
import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient } from "../api";
import type { BillAllocationRequest, CoredClient } from "../api";
import {
  _resetOutputTaxLedgerCache,
  _resetLedgerCache,
} from "./vouchers/gstVoucher";

afterEach(cleanup);
beforeEach(() => {
  _resetOutputTaxLedgerCache();
  _resetLedgerCache();
});

/**
 * BILL-WISE REFERENCES, DRIVEN THROUGH THE REAL SHELL.
 *
 * ⛔⛔ THIS FILE EXISTS BECAUSE THE FEATURE WAS UNREACHABLE FOR ITS ENTIRE LIFE.
 * `ledger.go` writes `bill_allocation` from `in.BillAllocations` — the CLIENT
 * supplies them on every path — and the only thing in the repository that ever
 * built one was `seed-demo.mts`. So every receivable keyed in a browser sat in
 * the Balance Sheet's Sundry Debtors and was ABSENT from Outstandings: money
 * that never ages and that nobody can chase. Every gate stayed green throughout,
 * because the demo's allocations were hand-written by the seed script.
 *
 * So the assertions here are all of one kind: **what does the form actually
 * SEND**, driven by keys and clicks through `<AppShell/>`, never by calling a
 * builder directly. A builder test cannot fail the way this feature failed.
 *
 * ⛔ AND EVERY ASSERTION CARRIES ITS CONTROL. "A reference was sent" is
 * consistent with a form that always sends one, which would attach bills to
 * parties that do not maintain them — so each test that asserts a reference is
 * paired with a party that must produce NONE.
 */

async function seedParties(base: CoredClient): Promise<void> {
  // The customer whose account is kept BILL BY BILL — the one that must produce
  // a reference.
  await base.createLedger("co-1", {
    name: "Acme Traders",
    group_name: "Sundry Debtors",
    state_code: "29",
    maintain_bill_by_bill: true,
  });
  // ⛔ THE CONTROL. Same group, same state, same everything — except the flag.
  // Without this party every assertion below would pass just as happily on a
  // form that attaches a bill reference to every voucher it sends.
  await base.createLedger("co-1", {
    name: "Cash Counter Co",
    group_name: "Sundry Debtors",
    state_code: "29",
    maintain_bill_by_bill: false,
  });
}

function spyClient(base: CoredClient): {
  client: CoredClient;
  spies: Record<string, ReturnType<typeof vi.fn>>;
} {
  const spies: Record<string, ReturnType<typeof vi.fn>> = {};
  const client = {} as Record<string, unknown>;
  for (const key of Object.keys(base) as (keyof CoredClient)[]) {
    const orig = base[key] as unknown as (...a: unknown[]) => unknown;
    const fn = vi.fn((...args: unknown[]) => orig.apply(base, args));
    spies[key] = fn;
    client[key] = fn;
  }
  return { client: client as unknown as CoredClient, spies };
}

function renderShell(client: CoredClient) {
  return render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: "co-1", name: "Test Co", homeStateCode: "29" }}
    >
      <AppShell />
    </CompanyProvider>,
  );
}

/** Key an F8 GST sale to `party` for `taxable` at 18%, and Ctrl+A it. */
async function keySale(
  app: HTMLElement,
  party: string,
  taxable: string,
): Promise<void> {
  fireEvent.mouseDown(await screen.findByText(party));
  fireEvent.change(screen.getByTestId("gst-taxable"), { target: { value: taxable } });
  fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });
  fireEvent.keyDown(app, { key: "a", ctrlKey: true });
}

describe("raising a bill — a sale to a bill-by-bill party", () => {
  it("SENDS a bill reference, unnamed, for the GROSS including tax", async () => {
    const base = createMockClient({ seed: false });
    await seedParties(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" });

    await keySale(app, "Acme Traders", "10000");

    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const [, req] = spies.postSalesVoucher!.mock.calls[0]!;
    const bills = (req as { bill_allocations?: BillAllocationRequest[] })
      .bill_allocations;
    expect(bills).toHaveLength(1);
    const bill = bills![0]!;
    // ⛔ THE GROSS, NOT THE TAXABLE BASE. The party is debited 11,800 and the
    // reference must total the posting exactly — the server refuses any other
    // figure, and a bill raised for 10,000 against an 11,800 invoice would leave
    // 1,800 of real money in Sundry Debtors that never ages.
    expect(bill.amount).toBe("11800.0000");
    expect(bill.dr_cr).toBe("DR");
    expect(bill.ref_type).toBe("NEW");
    // ⛔ UNNAMED ON PURPOSE. The voucher number is allocated inside the post
    // transaction, so the client cannot know it; the server stamps it.
    expect(bill.ref_name).toBe("");
  });

  it("⛔ CONTROL: the same sale to a party that is NOT bill-by-bill sends NONE", async () => {
    const base = createMockClient({ seed: false });
    await seedParties(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" });

    await keySale(app, "Cash Counter Co", "10000");

    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const [, req] = spies.postSalesVoucher!.mock.calls[0]!;
    expect(
      (req as { bill_allocations?: BillAllocationRequest[] }).bill_allocations,
    ).toBeUndefined();
    // And the panel is not even offered, so there is nothing to key by accident.
    expect(screen.queryByTestId("bill-refs-panel")).toBeNull();
  });

  it("shows the panel, defaulted in full, so the ordinary invoice needs no typing", async () => {
    const base = createMockClient({ seed: false });
    await seedParties(base);
    const { client } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" });

    // ⛔ ABSENT AS A CONTROL FIRST. Without this the appearance below could be
    // the panel having been there all along.
    expect(screen.queryByTestId("bill-refs-panel")).toBeNull();

    fireEvent.mouseDown(await screen.findByText("Acme Traders"));
    fireEvent.change(screen.getByTestId("gst-taxable"), { target: { value: "10000" } });
    fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });

    await screen.findByTestId("bill-refs-panel");
    await waitFor(() =>
      expect(screen.getByTestId("billrefs-remainder").textContent).toContain(
        "referenced in full",
      ),
    );
    // The reference names itself as server-numbered rather than offering a box.
    expect(screen.getByTestId("billrefs-autoname:0")).toBeTruthy();
  });

  it("REFUSES a short reference in the form, before the server has to", async () => {
    const base = createMockClient({ seed: false });
    await seedParties(base);
    const { client } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" });

    fireEvent.mouseDown(await screen.findByText("Acme Traders"));
    fireEvent.change(screen.getByTestId("gst-taxable"), { target: { value: "10000" } });
    fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });
    await screen.findByTestId("bill-refs-panel");

    // Knock the reference down below the invoice total.
    fireEvent.change(screen.getByTestId("billrefs-amount:0"), {
      target: { value: "9000" },
    });

    await waitFor(() =>
      expect(screen.getByTestId("billrefs-refusal").textContent).toContain(
        "short by 2800.0000",
      ),
    );
    expect(screen.getByTestId("billrefs-refusal").textContent).toContain(
      "never reach the Outstandings report",
    );
  });
});

describe("settling a bill — a receipt against an open invoice", () => {
  it("lists the party's OPEN BILLS and settles the one chosen, by name", async () => {
    const base = createMockClient({ seed: false });
    await seedParties(base);
    await base.createLedger("co-1", { name: "HDFC Bank", group_name: "Bank Accounts" });
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    // 1. Raise a bill: F8 sale of 10,000 + 18% to the bill-by-bill customer.
    fireEvent.keyDown(app, { key: "F8" });
    await keySale(app, "Acme Traders", "10000");
    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const raised = await spies.postSalesVoucher!.mock.results[0]!.value;
    const billName: string = raised.voucher_no;
    expect(billName).toBeTruthy();

    // 2. F6 Receipt, single entry: the BANK is the party field and the customer
    //    is a PARTICULAR — which is exactly why the picker hangs off the row.
    fireEvent.keyDown(app, { key: "F6" });
    await screen.findByTestId("single-entry-panel");
    fireEvent.mouseDown(await screen.findByText("HDFC Bank"));
    fireEvent.change(screen.getByTestId("single-ledger:0"), {
      target: { value: "Acme Traders" },
    });
    fireEvent.change(screen.getByTestId("single-amount:0"), {
      target: { value: "5000" },
    });

    // The settle picker appears ON THE ROW once the ledger resolves to a
    // bill-by-bill account, and it offers the bill just raised.
    await screen.findByTestId("billrefs");
    fireEvent.click(screen.getByTestId("billrefs-add"));
    await waitFor(() =>
      expect(screen.getByTestId("billrefs-bill:0")).toBeTruthy(),
    );
    fireEvent.change(screen.getByTestId("billrefs-bill:0"), {
      target: { value: billName },
    });
    // ⛔ THE PRE-FILL IS CAPPED AT WHAT IS LEFT ON THIS LINE, not the bill's full
    // 11,800 pending. A partial payment is the normal case, and pre-filling the
    // whole outstanding made the operator's first sight of the feature a refusal
    // about money they had not typed. Found by driving it in a browser.
    await waitFor(() =>
      expect(
        (screen.getByTestId("billrefs-amount:0") as HTMLInputElement).value,
      ).toBe("5000.0000"),
    );
    expect(screen.queryByTestId("billrefs-refusal")).toBeNull();

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() => expect(spies.postReceiptVoucher).toHaveBeenCalledTimes(1));
    const [, req] = spies.postReceiptVoucher!.mock.calls[0]!;
    const bills = (req as { bill_allocations?: BillAllocationRequest[] })
      .bill_allocations;
    expect(bills).toHaveLength(1);
    const settle = bills![0]!;
    // ⛔ IT NAMES THE BILL. Outstandings groups by (ledger, ref_name) and ignores
    // ref_type, so an unnamed settlement would open a SECOND bill rather than
    // reduce this one — leaving the invoice fully outstanding beside a negative
    // row, with both wrong and the report still footing.
    expect(settle.ref_name).toBe(billName);
    expect(settle.ref_type).toBe("AGAINST");
    expect(settle.dr_cr).toBe("CR");
    expect(settle.amount).toBe("5000.0000");
    // And it is attached to the CUSTOMER, never to the bank the money arrived in.
    const acme = (await base.listLedgers("co-1")).ledgers.find(
      (l) => l.name === "Acme Traders",
    )!;
    expect(settle.ledger_id).toBe(acme.id);
  });

  it("⛔ CONTROL: a particular that is NOT bill-by-bill offers no picker at all", async () => {
    const base = createMockClient({ seed: false });
    await seedParties(base);
    await base.createLedger("co-1", { name: "HDFC Bank", group_name: "Bank Accounts" });
    const { client } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F6" });
    await screen.findByTestId("single-entry-panel");
    fireEvent.mouseDown(await screen.findByText("HDFC Bank"));
    fireEvent.change(screen.getByTestId("single-ledger:0"), {
      target: { value: "Cash Counter Co" },
    });
    fireEvent.change(screen.getByTestId("single-amount:0"), {
      target: { value: "5000" },
    });

    // Give the resolution a chance to happen before asserting an absence.
    await waitFor(() =>
      expect(screen.getByTestId("single-amount:0")).toHaveProperty("value", "5000"),
    );
    expect(screen.queryByTestId("billrefs")).toBeNull();
  });
});

describe("the ALTER round trip — the data loss that was LIVE", () => {
  /**
   * ⛔⛔ `bill_allocation` IS KEYED ON `voucher_version_id`, SO AN ALTER
   * RE-STATES: whatever the body omits is GONE.
   *
   * `voucherFullJSON` has always RETURNED these rows — the read-back audit
   * checked exactly that and ticked them off — but until this change NO FORM SENT
   * THEM BACK. So editing the narration of any voucher carrying a bill reference
   * DELETED it: the invoice stayed in Sundry Debtors on the Balance Sheet and
   * vanished from Outstandings. Nothing refuses it, because zero references is a
   * legal state.
   *
   * The audit was half an audit, and that is the lesson: a GET that emits the
   * rows is NECESSARY and not SUFFICIENT.
   *
   * ⚠️ THIS TEST DRIVES THE FORM, and an earlier version of it did not — it
   * called `alterSalesVoucher` directly, which proved the STORE round-trips and
   * proved nothing at all about the screen. Deleting the hydration from
   * `formFromView` left it green. A test that cannot fail is worse than no test.
   */
  it("re-sends through the FORM what the GET returned, so an edit keeps the bill", async () => {
    const base = createMockClient({ seed: false });
    await seedParties(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    // Raise a real bill through the form.
    fireEvent.keyDown(app, { key: "F8" });
    await keySale(app, "Acme Traders", "10000");
    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const raised = await spies.postSalesVoucher!.mock.results[0]!.value;
    const billName: string = raised.voucher_no;

    // Drill Day Book -> the row -> the ALTER frame, the way an operator does.
    fireEvent.keyDown(app, { key: "Escape" });
    fireEvent.keyDown(app, { key: "g", altKey: true });
    fireEvent.mouseDown(screen.getByText("Day Book"));
    fireEvent.keyDown(app, { key: "Enter" });
    await waitFor(() => expect(screen.getByText(billName)).toBeTruthy());
    fireEvent.keyDown(app, { key: "Enter" });
    await waitFor(() =>
      expect(screen.getByText(/Sales Voucher \(Alteration\)/)).toBeTruthy(),
    );
    await waitFor(() => expect(spies.getVoucher).toHaveBeenCalled());

    // ⛔ THE HYDRATION IS VISIBLE: the panel comes back carrying the STORED
    // reference, named with the number the server stamped — not re-defaulted.
    await screen.findByTestId("bill-refs-panel");
    await waitFor(() =>
      expect(screen.getByTestId("billrefs-remainder").textContent).toContain(
        "referenced in full",
      ),
    );

    // Edit only the narration and save.
    fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.alterSalesVoucher).toHaveBeenCalledTimes(1));

    const [, , req] = spies.alterSalesVoucher!.mock.calls[0]!;
    const sent = (req as { bill_allocations?: BillAllocationRequest[] })
      .bill_allocations;
    expect(sent).toHaveLength(1);
    // ⛔ UNDER THE STORED NAME. A re-blanked name would let the server re-stamp —
    // a no-op only by luck, and on a renumbered voucher it opens a NEW bill while
    // leaving the original outstanding forever.
    expect(sent![0]!.ref_name).toBe(billName);
    expect(sent![0]!.amount).toBe("11800.0000");

    // And the store still holds exactly one reference afterwards.
    const kept = await base.getVoucher("co-1", raised.voucher_id);
    expect(kept.bill_allocations).toHaveLength(1);
    expect(kept.bill_allocations[0]!.ref_name).toBe(billName);
  });

  /**
   * ⛔⛔ THE SINGLE-ENTRY ALTER IS NOT TESTED HERE, AND THE REASON IS A SEPARATE
   * LIVE DEFECT — stated rather than papered over with a unit test that would
   * imply coverage this path does not have.
   *
   * `linesFromPostings` re-hydrates a PARTICULAR's references (a Receipt that
   * settles an invoice), and that mapping cannot be driven from the UI today:
   * `navMachine.ts` pushes `newVoucherFrame(DEFAULT_LAST_VOUCHER, "alter")` for
   * EVERY Day Book drill, so drilling into a Receipt opens a SALES frame. Proved
   * against the mock: drilling a Payment and pressing Ctrl+A calls
   * `alterSalesVoucher`, not `alterPaymentVoucher` — the voucher is re-classed.
   *
   * So the hydration is written and correct-by-inspection but UNREACHABLE, and a
   * test that called `linesFromPostings` directly would assert the mapping while
   * proving nothing about whether an operator can ever reach it — which is the
   * precise failure mode this whole file exists to guard against.
   *
   * ⚠️ WHEN THE DRILL DEFECT IS FIXED, ADD THE TEST: raise, settle 5,000 through
   * F6, drill the receipt, Ctrl+A, and assert the AGAINST reference survives.
   * Mutating the `billRefs` line in `linesFromPostings` to `[]` must fail it.
   */

  it("⛔ CONTROL: an alter that OMITS the references destroys them", async () => {
    // The hazard the test above depends on being real. Without this, "the alter
    // kept the bill" is equally consistent with references being immune to an
    // alter — in which case the hydration proves nothing.
    const base = createMockClient({ seed: false });
    await seedParties(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" });
    await keySale(app, "Acme Traders", "10000");
    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const raised = await spies.postSalesVoucher!.mock.results[0]!.value;

    const stored = await base.getVoucher("co-1", raised.voucher_id);
    expect(stored.bill_allocations).toHaveLength(1);

    await base.alterSalesVoucher("co-1", raised.voucher_id, {
      voucher_date: stored.voucher_date,
      narration: "typo fixed",
      party: stored.party,
      party_ledger_id: stored.party_ledger_id,
      postings: stored.postings.map((p) => ({
        ledger_id: p.ledger_id,
        dr_cr: p.dr_cr,
        amount: p.amount,
      })),
    });
    const wiped = await base.getVoucher("co-1", raised.voucher_id);
    expect(wiped.bill_allocations).toHaveLength(0);
  });
});
