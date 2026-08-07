import { describe, it, expect, afterEach, vi } from "vitest";
import {
  render,
  screen,
  fireEvent,
  cleanup,
  waitFor,
  act,
} from "@testing-library/react";
import { AppShell } from "./AppShell";
import { CompanyProvider } from "./CompanyContext";
import { createMockClient } from "../api";
import type { CoredClient, VoucherClass } from "../api";

afterEach(cleanup);

/**
 * Wrap a real (in-memory) mock client so each method is a vi.fn spy that still
 * delegates to the faithful mock — so we can assert which method fired with what
 * arguments AND keep the mock's numbering/day-book behavior for drill tests.
 */
function spyClient(base: CoredClient = createMockClient({ seed: false })): {
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
      initialCompany={{ id: "co-1", name: "Test Co" }}
    >
      <AppShell />
    </CompanyProvider>,
  );
}

async function seedSalesVoucher(client: CoredClient) {
  const debtor = await client.createLedger("co-1", {
    name: "Acme Corp",
    group_name: "Sundry Debtors",
  });
  const sales = await client.createLedger("co-1", {
    name: "Domestic Sales",
    group_name: "Sales Accounts",
  });
  const posted = await client.postSalesVoucher("co-1", {
    voucher_no: "INV/0001",
    voucher_date: "2026-04-10",
    party: "Acme Corp",
    postings: [
      { ledger_id: debtor.ledger_id, dr_cr: "DR", amount: "400.0000" },
      { ledger_id: sales.ledger_id, dr_cr: "CR", amount: "400.0000" },
    ],
  });
  return { debtor, sales, posted };
}

describe("VoucherScreen Ctrl+A create wiring", () => {
  const cases: { fkey: string; cls: VoucherClass; method: string }[] = [
    { fkey: "F8", cls: "sales", method: "postSalesVoucher" },
    { fkey: "F9", cls: "purchase", method: "postPurchaseVoucher" },
    { fkey: "F6", cls: "receipt", method: "postReceiptVoucher" },
    { fkey: "F5", cls: "payment", method: "postPaymentVoucher" },
    { fkey: "F4", cls: "contra", method: "postContraVoucher" },
    { fkey: "F7", cls: "journal", method: "postJournalVoucher" },
  ];

  // Payment / Receipt / Contra open in SINGLE-ENTRY mode (Tally's default for
  // those three), which requires an account and at least one particular. They
  // are filled here the way an operator fills them.
  const SINGLE_ENTRY: Record<string, boolean> = {
    receipt: true,
    payment: true,
    contra: true,
  };

  // ⛔⛔ AND THE JOURNAL NOW NEEDS ITS GRID FILLED — WHICH IS THE POINT.
  //
  // THIS CASE USED TO PASS ON AN EMPTY FORM, AND THAT WAS THE BUG IT CERTIFIED.
  // F7's `double` branch rendered no posting grid at all, so pressing Ctrl+A on
  // an untouched Journal called `postJournalVoucher` with `postings: []` — and
  // this test asserted exactly that as correct behaviour ("the other classes
  // still accept an empty form, which is the pre-existing behaviour").
  //
  // A Journal with no postings is not a voucher. It was the most-cited hidden
  // parity gap on the board (HANDOFF §4.5 / §5.1: a row reading `on` that could
  // not be used at all), and the suite was holding it in place. This is
  // HANDOFF §9.1's "a test can certify a bug" for the second recorded time —
  // the first was the order book's `As at ${book.as_on}` heading passing on an
  // empty string.
  //
  // The grid now refuses an unbalanced or empty body IN THE FORM, so this case
  // states a real two-line entry. The refusal itself is covered in
  // VoucherScreen.doubleEntry.test.tsx.
  const DOUBLE_ENTRY: Record<string, boolean> = { journal: true };

  for (const { fkey, cls, method } of cases) {
    it(`Ctrl+A in a ${cls} voucher calls ${method} with empty voucher_no and shows the returned number`, async () => {
      const { client, spies } = spyClient();
      renderShell(client);
      const app = screen.getByRole("application");
      fireEvent.keyDown(app, { key: fkey });

      if (SINGLE_ENTRY[cls]) {
        fireEvent.change(screen.getByRole("combobox", { name: "Account" }), {
          target: { value: "HDFC Bank" },
        });
        fireEvent.mouseDown(await screen.findByText(/HDFC Bank/));
        fireEvent.change(screen.getByTestId("single-ledger:0"), {
          target: { value: "Office Rent" },
        });
        fireEvent.change(screen.getByTestId("single-amount:0"), {
          target: { value: "2500" },
        });
      }

      if (DOUBLE_ENTRY[cls]) {
        await screen.findByTestId("double-entry-panel");
        fireEvent.change(screen.getByTestId("de-ledger:0"), {
          target: { value: "Depreciation" },
        });
        fireEvent.change(screen.getByTestId("de-side:0"), {
          target: { value: "DR" },
        });
        fireEvent.change(screen.getByTestId("de-amount:0"), {
          target: { value: "1250" },
        });
        fireEvent.click(screen.getByTestId("de-add-line"));
        fireEvent.change(screen.getByTestId("de-ledger:1"), {
          target: { value: "Accumulated Depreciation" },
        });
        fireEvent.change(screen.getByTestId("de-side:1"), {
          target: { value: "CR" },
        });
        fireEvent.change(screen.getByTestId("de-amount:1"), {
          target: { value: "1250" },
        });
      }

      // Accept the voucher.
      fireEvent.keyDown(app, { key: "a", ctrlKey: true });

      await waitFor(() => expect(spies[method]).toHaveBeenCalledTimes(1));
      const [companyID, req] = spies[method]!.mock.calls[0]!;
      expect(companyID).toBe("co-1");
      // Create mode: the client is asked WITHOUT a voucher_no (server allocates).
      expect((req as { voucher_no?: string }).voucher_no).toBeUndefined();
      // The server-allocated number is surfaced in the form.
      await waitFor(() =>
        expect(screen.getByTestId("voucher-no").textContent).toMatch(/No: \S+/),
      );
    });
  }
});

describe("VoucherScreen Ctrl+A alter wiring (drill-loaded)", () => {
  it("resends the loaded voucher_no/date and calls alterSalesVoucher with the loaded id", async () => {
    const base = createMockClient({ seed: false });
    const { posted } = await seedSalesVoucher(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    // Drill: Day Book -> highlight the row -> Enter into the alter voucher frame.
    fireEvent.keyDown(app, { key: "g", altKey: true });
    fireEvent.mouseDown(screen.getByText("Day Book"));
    fireEvent.keyDown(app, { key: "Enter" }); // open Day Book report
    await waitFor(() => expect(spies.getDayBook).toHaveBeenCalled());
    // The Day Book row highlight publishes the loaded voucher; Enter drills in.
    await waitFor(() => expect(screen.getByText("INV/0001")).toBeTruthy());
    fireEvent.keyDown(app, { key: "Enter" }); // drill into the voucher (alter)

    // The alter form is pre-filled with the loaded number.
    await waitFor(() =>
      expect(screen.getByText(/Sales Voucher \(Alteration\)/)).toBeTruthy(),
    );
    expect(screen.getByTestId("voucher-no-field").textContent).toBe("INV/0001");

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.alterSalesVoucher).toHaveBeenCalledTimes(1));
    const [companyID, voucherID, req] =
      spies.alterSalesVoucher!.mock.calls[0]!;
    expect(companyID).toBe("co-1");
    expect(voucherID).toBe(posted.voucher_id);
    expect((req as { voucher_no?: string }).voucher_no).toBe("INV/0001");
    expect((req as { voucher_date?: string }).voucher_date).toBe("2026-04-10");
  });
});

describe("VoucherScreen Alt+D delete wiring", () => {
  it("calls deleteVoucher with the loaded id in alter mode", async () => {
    const base = createMockClient({ seed: false });
    const { posted } = await seedSalesVoucher(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "g", altKey: true });
    fireEvent.mouseDown(screen.getByText("Day Book"));
    fireEvent.keyDown(app, { key: "Enter" });
    await waitFor(() => expect(screen.getByText("INV/0001")).toBeTruthy());
    fireEvent.keyDown(app, { key: "Enter" }); // drill into alter voucher
    await waitFor(() =>
      expect(screen.getByText(/Sales Voucher \(Alteration\)/)).toBeTruthy(),
    );

    fireEvent.keyDown(app, { key: "d", altKey: true });
    await waitFor(() => expect(spies.deleteVoucher).toHaveBeenCalledTimes(1));
    expect(spies.deleteVoucher!.mock.calls[0]![1]).toBe(posted.voucher_id);
  });

  it("is a no-op in create mode (deleteVoucher not called)", async () => {
    const { client, spies } = spyClient();
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" }); // fresh create-mode sales voucher
    fireEvent.keyDown(app, { key: "d", altKey: true });
    // Give any (erroneous) effect a tick.
    await act(async () => {
      await Promise.resolve();
    });
    expect(spies.deleteVoucher).not.toHaveBeenCalled();
  });
});

describe("MasterCreate Alt+C wiring", () => {
  it("Ctrl+A on an inline ledger create calls createLedger and returns the name to the voucher", async () => {
    const { client, spies } = spyClient();
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" }); // sales voucher
    fireEvent.keyDown(app, { key: "c", altKey: true }); // Alt+C -> create ledger

    await waitFor(() =>
      expect(screen.getByText(/Create ledger/)).toBeTruthy(),
    );
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "New Debtor" },
    });
    fireEvent.mouseDown(screen.getByText("Sundry Debtors"));
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() => expect(spies.createLedger).toHaveBeenCalledTimes(1));
    const [companyID, req] = spies.createLedger!.mock.calls[0]!;
    expect(companyID).toBe("co-1");
    expect((req as { name: string }).name).toBe("New Debtor");
    expect((req as { group_name: string }).group_name).toBe("Sundry Debtors");
    // The machine popped back to the voucher with the created name as the pick.
    await waitFor(() =>
      expect(screen.getByText(/Sales Voucher/)).toBeTruthy(),
    );
  });
});

describe("CompanyContext bootstrap", () => {
  it("does NOT createCompany when an explicit company is provided", async () => {
    const { client, spies } = spyClient(createMockClient());
    renderShell(client);
    await act(async () => {
      await Promise.resolve();
    });
    expect(spies.createCompany).not.toHaveBeenCalled();
    expect(screen.getByText("Gateway of Tally")).toBeTruthy();
  });
});
