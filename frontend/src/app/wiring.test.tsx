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

  for (const { fkey, cls, method } of cases) {
    it(`Ctrl+A in a ${cls} voucher calls ${method} with empty voucher_no and shows the returned number`, async () => {
      const { client, spies } = spyClient();
      renderShell(client);
      const app = screen.getByRole("application");
      fireEvent.keyDown(app, { key: fkey });
      // Accept the (empty) voucher.
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
