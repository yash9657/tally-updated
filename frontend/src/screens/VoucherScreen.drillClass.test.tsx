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
 * ⛔⛔ DRILLING INTO A VOUCHER OPENS ITS OWN CLASS — §4.8, DRIVEN THROUGH THE
 * REAL SHELL.
 *
 * ## The defect
 *
 * `navMachine` pushed `newVoucherFrame(DEFAULT_LAST_VOUCHER, "alter")` for every
 * voucher-bearing drill, and `DEFAULT_LAST_VOUCHER` is `"sales"`. The machine had
 * only the row's voucher id, so it could not know the class. `acceptVoucher` then
 * routes the PUT by **`ctx.type`** — the SCREEN's type, not the loaded voucher's
 * class. So a Receipt / Payment / Contra / Journal opened from the Day Book and
 * saved was written back through `alterSalesVoucher`: **a corrupted document.**
 * The frame even titled itself "Sales Voucher (Alteration)" over `RCPT/1`, and
 * the Sales GST form rendered over a single-entry voucher, so its particulars
 * were not editable.
 *
 * ## Why the assertions are what they are
 *
 * ⛔ THE ROUTED METHOD IS THE ASSERTION THAT MATTERS, and nothing cheaper
 * substitutes for it. `phase1.test.ts` pins the FRAME's type against the pure
 * machine — necessary, and not sufficient: the corruption lives in
 * `acceptVoucher`'s dispatch, one layer below the frame, and a machine test
 * cannot see it. §9.1a item 4 is the rule this file obeys — the earlier
 * bill-reference round-trip test called the store API directly and stayed green
 * when the hydration it existed to protect was deleted.
 *
 * ⛔ AND SALES IS DRIVEN LAST, DELIBERATELY. It is the ONE class for which the
 * old default was accidentally right, so a suite that only exercised Sales
 * certified the bug — which is exactly what happened: §5.5 listed the Day Book as
 * "known-good, driven end to end", and it was, with a Sales voucher (§9.1a item
 * 7). A parameterised screen needs more than one value of the parameter.
 */

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

async function seedAccounts(base: CoredClient): Promise<void> {
  await base.createLedger("co-1", { name: "HDFC Bank", group_name: "Bank Accounts" });
  await base.createLedger("co-1", { name: "Cash", group_name: "Cash-in-Hand" });
  await base.createLedger("co-1", { name: "Electricity", group_name: "Indirect Expenses" });
  await base.createLedger("co-1", {
    name: "Acme Traders",
    group_name: "Sundry Debtors",
    state_code: "29",
    maintain_bill_by_bill: true,
  });
}

/** Fill the single-entry form: one account, then one particular. */
async function fillSingleEntry(
  account: string,
  ledger: string,
  amount: string,
): Promise<void> {
  await screen.findByTestId("single-entry-panel");
  fireEvent.mouseDown(await screen.findByText(new RegExp(`^${account}$`)));
  fireEvent.change(screen.getByTestId("single-ledger:0"), {
    target: { value: ledger },
  });
  fireEvent.change(screen.getByTestId("single-amount:0"), {
    target: { value: amount },
  });
}

/** Day Book -> highlight the only row -> Enter, the way an operator drills. */
async function drillTheOnlyDayBookRow(
  app: HTMLElement,
  voucherNo: string,
): Promise<void> {
  fireEvent.keyDown(app, { key: "Escape" });
  fireEvent.keyDown(app, { key: "g", altKey: true });
  fireEvent.mouseDown(screen.getByText("Day Book"));
  fireEvent.keyDown(app, { key: "Enter" });
  // Wait on the RENDERED ROW, never on a spy: a spy resolves when the fetch was
  // invoked, not when its data reached the grid, and DrillRows publishes the row
  // key from an effect that runs after that render (§9.1a item 5).
  await waitFor(() => expect(screen.getByText(voucherNo)).toBeTruthy());
  fireEvent.keyDown(app, { key: "Enter" });
}

describe("a Day Book drill opens the voucher's OWN class", () => {
  for (const [fkey, cls, alterMethod, account] of [
    ["F6", "Receipt", "alterReceiptVoucher", "HDFC Bank"],
    ["F5", "Payment", "alterPaymentVoucher", "HDFC Bank"],
    ["F4", "Contra", "alterContraVoucher", "HDFC Bank"],
    // Sales LAST — the one class the old default got right by accident.
    ["F8", "Sales", "alterSalesVoucher", ""],
  ] as const) {
    it(`${fkey} ${cls}: Ctrl+A on the drilled frame calls ${alterMethod}`, async () => {
      const base = createMockClient({ seed: false });
      await seedAccounts(base);
      const { client, spies } = spyClient(base);
      renderShell(client);
      const app = screen.getByRole("application");

      // 1. Post a real voucher of this class through the form.
      fireEvent.keyDown(app, { key: fkey });
      if (cls === "Sales") {
        fireEvent.mouseDown(await screen.findByText("Acme Traders"));
        fireEvent.change(screen.getByTestId("gst-taxable"), {
          target: { value: "10000" },
        });
        fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });
        // ⛔ THE HSN IS NOW MANDATORY TO SAVE A SALES INVOICE (D60) — it is a Rule
        // 46(g) particular, and without it the invoice saved and then could not be
        // PRINTED. Keying it makes every assertion below the CONTROL for that new
        // refusal: an invoice that DOES carry an HSN still saves, unchanged.
        fireEvent.change(screen.getByTestId("gst-hsn"), { target: { value: "7326" } });
      } else if (cls === "Contra") {
        // Cash <-> Bank only; an expense ledger is refused on this class.
        await fillSingleEntry(account, "Cash", "2500");
      } else {
        await fillSingleEntry(account, "Electricity", "2500");
      }
      fireEvent.keyDown(app, { key: "a", ctrlKey: true });

      const postMethod = `post${cls.replace(/\s/g, "")}Voucher`;
      await waitFor(() => expect(spies[postMethod]).toHaveBeenCalledTimes(1));
      const posted = await spies[postMethod]!.mock.results[0]!.value;

      // 2. Drill it from the Day Book.
      await drillTheOnlyDayBookRow(app, posted.voucher_no);

      // ⛔ THE TITLE FOLLOWS THE DOCUMENT. `AppShell.screenTitle` reads
      // `snap.voucherType` off the FRAME, so this assertion is only satisfiable
      // by the frame itself carrying the class — which is why the fix puts the
      // class in the row key rather than only in acceptVoucher's dispatch.
      await waitFor(() =>
        expect(
          screen.getAllByText(new RegExp(`${cls} Voucher \\(Alteration\\)`)).length,
        ).toBeGreaterThan(0),
      );
      await waitFor(() => expect(spies.getVoucher).toHaveBeenCalled());

      // 3. Ctrl+A. THE ROUTED METHOD IS THE POINT.
      fireEvent.keyDown(app, { key: "a", ctrlKey: true });
      await waitFor(() => expect(spies[alterMethod]).toHaveBeenCalledTimes(1));
      // And the id it altered is the one drilled — not a new voucher.
      expect(spies[alterMethod]!.mock.calls[0]![1]).toBe(posted.voucher_id);

      // ⛔ THE CONTROL, and without it every row above passes on a build that
      // routes EVERYTHING through alterSalesVoucher: for the three non-Sales
      // classes the Sales alter must NOT have been called at all.
      if (cls !== "Sales") {
        expect(spies.alterSalesVoucher).not.toHaveBeenCalled();
      }

      // The class did not change in the store either: an alter that re-classed
      // would leave the Day Book naming a different document.
      const after = await base.getVoucher("co-1", posted.voucher_id);
      expect(after.class).toBe(cls);
    });
  }

  /**
   * ⛔ THE SINGLE-ENTRY FORM RENDERS, which is the second consequence §4.8 lists
   * and the one that blocked other work. A Receipt drilled into a Sales frame
   * rendered the GST invoice form over a single-entry voucher, so the operator
   * could not edit the particulars of the voucher they had just opened — and it
   * made the single-entry bill-reference hydration unreachable and therefore
   * unverified (§1.5).
   */
  it("renders the SINGLE-ENTRY form over a drilled Receipt, not the GST form", async () => {
    const base = createMockClient({ seed: false });
    await seedAccounts(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F6" });
    await fillSingleEntry("HDFC Bank", "Electricity", "2500");
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.postReceiptVoucher).toHaveBeenCalledTimes(1));
    const posted = await spies.postReceiptVoucher!.mock.results[0]!.value;

    await drillTheOnlyDayBookRow(app, posted.voucher_no);

    expect(await screen.findByTestId("single-entry-panel")).toBeTruthy();
    // The POSITIVE half: the GST panel the Sales frame would have shown is absent.
    expect(screen.queryByTestId("gst-taxable")).toBeNull();
  });

  /**
   * ⛔⛔ THE TEST §1.5 PROMISED, AND IT COULD NOT BE WRITTEN UNTIL NOW.
   *
   * `linesFromPostings` re-hydrates a PARTICULAR's bill references — a Receipt
   * that settles an invoice — and `VoucherScreen.billRefs.test.tsx` says at the
   * point where this test belongs that the mapping was "written and correct-by-
   * inspection but UNREACHABLE", because you could not drill into a Receipt at
   * all. That was the third consequence of §4.8, and this is the test it named:
   *
   *   > raise, settle 5,000 through F6, drill the receipt, Ctrl+A, and assert the
   *   > AGAINST reference survives. Mutating the `billRefs` line in
   *   > `linesFromPostings` to `[]` must fail it.
   *
   * ⛔ IT MATTERS BECAUSE `bill_allocation` IS KEYED ON `voucher_version_id`, SO
   * AN ALTER RE-STATES: an alter that omits the reference DELETES it, and the
   * settled invoice silently springs back to fully outstanding while Sundry
   * Debtors does not move. Zero references is a legal state, so nothing refuses
   * it — see §4.3, whose audit cleared this very table on half a check.
   */
  it("⛔ an alter of a drilled RECEIPT re-sends the AGAINST reference it settled", async () => {
    const base = createMockClient({ seed: false });
    await seedAccounts(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    // 1. Raise the bill.
    fireEvent.keyDown(app, { key: "F8" });
    fireEvent.mouseDown(await screen.findByText("Acme Traders"));
    fireEvent.change(screen.getByTestId("gst-taxable"), { target: { value: "10000" } });
    fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });
    // ⛔ THE HSN IS NOW MANDATORY TO SAVE A SALES INVOICE (D60) — it is a Rule
    // 46(g) particular, and without it the invoice saved and then could not be
    // PRINTED. Keying it makes every assertion below the CONTROL for that new
    // refusal: an invoice that DOES carry an HSN still saves, unchanged.
    fireEvent.change(screen.getByTestId("gst-hsn"), { target: { value: "7326" } });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const billName: string = (
      await spies.postSalesVoucher!.mock.results[0]!.value
    ).voucher_no;

    // 2. Settle 5,000 of it through F6 — the bank is the party, the customer is
    //    a PARTICULAR, so the reference hangs off the row.
    fireEvent.keyDown(app, { key: "F6" });
    await fillSingleEntry("HDFC Bank", "Acme Traders", "5000");
    await screen.findByTestId("billrefs");
    fireEvent.click(screen.getByTestId("billrefs-add"));
    await waitFor(() => expect(screen.getByTestId("billrefs-bill:0")).toBeTruthy());
    fireEvent.change(screen.getByTestId("billrefs-bill:0"), {
      target: { value: billName },
    });
    await waitFor(() =>
      expect(
        (screen.getByTestId("billrefs-amount:0") as HTMLInputElement).value,
      ).toBe("5000.0000"),
    );
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.postReceiptVoucher).toHaveBeenCalledTimes(1));
    const receipt = await spies.postReceiptVoucher!.mock.results[0]!.value;

    // 3. Drill the RECEIPT — impossible before this change — and alter it.
    fireEvent.keyDown(app, { key: "Escape" });
    fireEvent.keyDown(app, { key: "g", altKey: true });
    fireEvent.mouseDown(screen.getByText("Day Book"));
    fireEvent.keyDown(app, { key: "Enter" });
    await waitFor(() => expect(screen.getByText(receipt.voucher_no)).toBeTruthy());
    // The receipt is the SECOND row (the sale came first, same date), so walk the
    // row cursor down before drilling.
    fireEvent.keyDown(screen.getByRole("grid", { name: /Day Book/i }), {
      key: "ArrowDown",
    });
    fireEvent.keyDown(app, { key: "Enter" });
    await waitFor(() =>
      expect(screen.getAllByText(/Receipt Voucher \(Alteration\)/).length)
        .toBeGreaterThan(0),
    );

    // ⛔ THE HYDRATION IS VISIBLE ON THE ROW, carrying the STORED name.
    await waitFor(() =>
      expect(
        (screen.getByTestId("billrefs-amount:0") as HTMLInputElement).value,
      ).toBe("5000.0000"),
    );

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.alterReceiptVoucher).toHaveBeenCalledTimes(1));
    const [, , req] = spies.alterReceiptVoucher!.mock.calls[0]!;
    const sent = (req as { bill_allocations?: BillAllocationRequest[] })
      .bill_allocations;
    expect(sent).toHaveLength(1);
    expect(sent![0]!.ref_name).toBe(billName);
    expect(sent![0]!.ref_type).toBe("AGAINST");
    expect(sent![0]!.amount).toBe("5000.0000");

    // And the store still holds it, so the invoice is still settled.
    const kept = await base.getVoucher("co-1", receipt.voucher_id);
    expect(kept.bill_allocations).toHaveLength(1);
    expect(kept.bill_allocations[0]!.ref_name).toBe(billName);
  });
});

/**
 * ⛔⛔ PAGE UP / PAGE DOWN — THE SECOND LIVE PATH, AND IT IS NOT IN §4.8.
 *
 * `stepVoucher` swaps the loaded voucher UNDERNEATH a standing alteration frame,
 * and the Day Book is chronological and therefore MIXED-CLASS. So paging from a
 * Sales alteration onto a Receipt re-classed the receipt on Ctrl+A with no drill
 * involved at all — the identical corruption reached by a different key. A fix
 * that lived only in the drill would have left this one live.
 */
describe("Page Up / Page Down re-classes the standing frame", () => {
  it("paging from a drilled SALE onto a RECEIPT routes Ctrl+A to the receipt", async () => {
    const base = createMockClient({ seed: false });
    await seedAccounts(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    // A sale, then a receipt — two Day Book rows, in that order.
    fireEvent.keyDown(app, { key: "F8" });
    fireEvent.mouseDown(await screen.findByText("Acme Traders"));
    fireEvent.change(screen.getByTestId("gst-taxable"), { target: { value: "10000" } });
    fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });
    // ⛔ THE HSN IS NOW MANDATORY TO SAVE A SALES INVOICE (D60) — it is a Rule
    // 46(g) particular, and without it the invoice saved and then could not be
    // PRINTED. Keying it makes every assertion below the CONTROL for that new
    // refusal: an invoice that DOES carry an HSN still saves, unchanged.
    fireEvent.change(screen.getByTestId("gst-hsn"), { target: { value: "7326" } });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const sale = await spies.postSalesVoucher!.mock.results[0]!.value;

    fireEvent.keyDown(app, { key: "F6" });
    await fillSingleEntry("HDFC Bank", "Electricity", "2500");
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.postReceiptVoucher).toHaveBeenCalledTimes(1));
    const receipt = await spies.postReceiptVoucher!.mock.results[0]!.value;

    // Drill the SALE, then Page Down onto the receipt.
    await drillTheOnlyDayBookRow(app, sale.voucher_no);
    await waitFor(() =>
      expect(screen.getAllByText(/Sales Voucher \(Alteration\)/).length)
        .toBeGreaterThan(0),
    );
    fireEvent.keyDown(app, { key: "PageDown" });

    // The frame became a RECEIPT alteration, titled as one.
    await waitFor(() =>
      expect(screen.getAllByText(/Receipt Voucher \(Alteration\)/).length)
        .toBeGreaterThan(0),
    );
    await screen.findByTestId("single-entry-panel");

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.alterReceiptVoucher).toHaveBeenCalledTimes(1));
    expect(spies.alterReceiptVoucher!.mock.calls[0]![1]).toBe(receipt.voucher_id);
    // ⛔ THE CONTROL: the Sales alter — which is what this used to call — was not
    // touched. Without it the assertion above is consistent with a build that
    // calls both.
    expect(spies.alterSalesVoucher).not.toHaveBeenCalled();
  });

  /**
   * ⛔⛔ PAGING DOES NOT DEAD-END ON A STOCK CLASS — a defect this change ITSELF
   * created, found by paging in a real browser and not by any review.
   *
   * `stepVoucher` used to live inside `AccountingVoucherScreen`, which was
   * harmless only while the frame's class never changed: paging always kept
   * rendering the accounting form, so the handler was always mounted. Now that the
   * frame follows the document, paging onto a Delivery Note correctly mounts
   * `StockVoucherScreen` — and that component registers only `accept`. So Page Up
   * AND Page Down both went dead and the operator was STRANDED on that voucher,
   * with Esc the only way out.
   *
   * Observed in the browser as Sales → Purchase → Receipt Note, then five presses
   * that did nothing whatever. The fix registers paging on the ROUTER, above the
   * split; this test is what stops it drifting back into either form.
   */
  it("⛔ paging ONWARD off a stock-class voucher still works (the dead end)", async () => {
    const base = createMockClient({ seed: false });
    await seedAccounts(base);
    const item = (
      await base.createStockItem("co-1", {
        name: "Widget",
        unit_symbol: "Nos",
        hsn: "8481",
        gst_rate_pct: "18.0000",
      })
    ).stock_item_id;
    const goods = { stock_item_id: item, direction: "IN" as const, qty: "5.0000", rate: "10.0000" };

    // Three vouchers in Day Book order: a receipt NOTE (stock) between two
    // accounting vouchers, so paging has to cross it and come out the far side.
    const { ledgers } = await base.listLedgers("co-1");
    const idOf = (name: string) => ledgers.find((l) => l.name === name)!.id;
    const first = await base.postPaymentVoucher("co-1", {
      voucher_date: "2026-04-01",
      // ⚠️ THE PARTY IS STATED. A single-entry voucher stored with no `party`
      // hydrates an empty Account field and `buildSingleEntryRequest` refuses it
      // by name — a correct refusal, and one that would make the Ctrl+A assertion
      // below fail for a reason that has nothing to do with paging.
      party: "HDFC Bank",
      party_ledger_id: idOf("HDFC Bank"),
      postings: [
        { ledger_id: idOf("HDFC Bank"), dr_cr: "CR", amount: "100.0000" },
        { ledger_id: idOf("Electricity"), dr_cr: "DR", amount: "100.0000" },
      ],
    });
    const middle = await base.postReceiptNoteVoucher("co-1", {
      voucher_date: "2026-04-02",
      inventory: [goods],
    });
    const last = await base.postContraVoucher("co-1", {
      voucher_date: "2026-04-03",
      party: "HDFC Bank",
      party_ledger_id: idOf("HDFC Bank"),
      postings: [
        { ledger_id: idOf("HDFC Bank"), dr_cr: "CR", amount: "50.0000" },
        { ledger_id: idOf("Cash"), dr_cr: "DR", amount: "50.0000" },
      ],
    });

    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    await drillTheOnlyDayBookRow(app, first.voucher_no);
    await waitFor(() =>
      expect(screen.getAllByText(/Payment Voucher \(Alteration\)/).length)
        .toBeGreaterThan(0),
    );

    // Onto the stock voucher: the STOCK screen mounts, which is correct.
    fireEvent.keyDown(app, { key: "PageDown" });
    await waitFor(() =>
      expect(screen.getAllByText(/Receipt Note \(Alteration\)/).length)
        .toBeGreaterThan(0),
    );
    expect(middle.voucher_id).toBeTruthy();

    // ⛔ AND PAGING ONWARD FROM THERE STILL MOVES. This is the assertion the
    // browser earned: before the fix it stayed on the Receipt Note forever.
    fireEvent.keyDown(app, { key: "PageDown" });
    await waitFor(() =>
      expect(screen.getAllByText(/Contra Voucher \(Alteration\)/).length)
        .toBeGreaterThan(0),
    );

    // And it pages BACK out of the stock voucher too — the dead end trapped both
    // directions, so asserting only one of them would leave half of it live.
    fireEvent.keyDown(app, { key: "PageUp" });
    await waitFor(() =>
      expect(screen.getAllByText(/Receipt Note \(Alteration\)/).length)
        .toBeGreaterThan(0),
    );
    fireEvent.keyDown(app, { key: "PageUp" });
    await waitFor(() =>
      expect(screen.getAllByText(/Payment Voucher \(Alteration\)/).length)
        .toBeGreaterThan(0),
    );

    // Ctrl+A on the far side routes to the class it landed on, not to the one it
    // started from — the whole point of paging carrying the class.
    expect(last.voucher_id).toBeTruthy();
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.alterPaymentVoucher).toHaveBeenCalledTimes(1));
    expect(spies.alterSalesVoucher).not.toHaveBeenCalled();
  });
});
