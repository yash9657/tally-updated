import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";
import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient } from "../api";
import type { CoredClient, GSTRequest, PostingRequest } from "../api";
import {
  _resetOutputTaxLedgerCache,
  _resetLedgerCache,
} from "./vouchers/gstVoucher";

afterEach(cleanup);
beforeEach(() => {
  _resetOutputTaxLedgerCache();
  _resetLedgerCache();
});

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
      initialCompany={{ id: "co-1", name: "Test Co", homeStateCode: "29" }}
    >
      <AppShell />
    </CompanyProvider>,
  );
}

describe("Sales voucher GST form", () => {
  it("sends a well-formed GSTRequest with the derived intra-state tax lines on Ctrl+A", async () => {
    const { client, spies } = spyClient();
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" }); // sales voucher (create)

    // Choose a party (state 29 -> intra-state vs home 29) and a taxable base+rate.
    fireEvent.mouseDown(screen.getByText("Acme Traders"));
    fireEvent.change(screen.getByTestId("gst-taxable"), {
      target: { value: "10000" },
    });
    fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const [, req] = spies.postSalesVoucher!.mock.calls[0]!;
    const gst = (req as { gst: GSTRequest }).gst;
    expect(gst.supplier_state_code).toBe("29");
    expect(gst.place_of_supply_state_code).toBe("29");
    expect(gst.lines?.[0]?.taxable_value).toBe("10000.0000");
    expect(gst.lines?.[0]?.gst_rate_pct).toBe("18");
    expect(gst.cgst_ledger_id).toBeTruthy();
    expect(gst.sgst_ledger_id).toBeTruthy();
    expect(gst.igst_ledger_id).toBeTruthy();

    const postings = (req as { postings: PostingRequest[] }).postings;
    // party Dr 11800 + income Cr 10000 + CGST 900 + SGST 900 (intra), no IGST.
    expect(postings).toHaveLength(4);
    const amounts = postings.map((p) => p.amount);
    expect(amounts).toContain("11800.0000");
    expect(amounts).toContain("10000.0000");
    expect(amounts.filter((a) => a === "900.0000")).toHaveLength(2);

    // The Tax panel renders the SERVER-returned split (mock echoes 900/900).
    await waitFor(() =>
      expect(screen.getByTestId("tax-cgst").textContent).toBe("900.0000"),
    );
    expect(screen.getByTestId("tax-sgst").textContent).toBe("900.0000");
    expect(screen.queryByTestId("tax-igst")).toBeNull();
  });

  it("inter-state party emits a single IGST line, no CGST/SGST", async () => {
    const { client, spies } = spyClient();
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" });

    fireEvent.mouseDown(screen.getByText("Bharat Stores")); // state 27 vs home 29
    fireEvent.change(screen.getByTestId("gst-taxable"), {
      target: { value: "10000" },
    });
    fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const [, req] = spies.postSalesVoucher!.mock.calls[0]!;
    const postings = (req as { postings: PostingRequest[] }).postings;
    expect(postings).toHaveLength(3);
    expect(postings.map((p) => p.amount)).toContain("1800.0000");

    await waitFor(() =>
      expect(screen.getByTestId("tax-igst").textContent).toBe("1800.0000"),
    );
    expect(screen.queryByTestId("tax-cgst")).toBeNull();
    expect(screen.queryByTestId("tax-sgst")).toBeNull();
  });
});

describe("drill-to-alter hydration via getVoucher", () => {
  it("seeds the alter form from REAL posting uuids so the alter PUT carries uuids", async () => {
    const base = createMockClient({ seed: false });
    const debtor = await base.createLedger("co-1", {
      name: "Acme Corp",
      group_name: "Sundry Debtors",
      state_code: "29",
    });
    const sales = await base.createLedger("co-1", {
      name: "Domestic Sales",
      group_name: "Sales Accounts",
    });
    const cgst = await base.createLedger("co-1", {
      name: "Output CGST",
      group_name: "Duties & Taxes",
    });
    const sgst = await base.createLedger("co-1", {
      name: "Output SGST",
      group_name: "Duties & Taxes",
    });
    const igst = await base.createLedger("co-1", {
      name: "Output IGST",
      group_name: "Duties & Taxes",
    });
    const posted = await base.postSalesVoucher("co-1", {
      voucher_no: "INV/0001",
      voucher_date: "2025-05-10",
      party: "Acme Corp",
      party_ledger_id: debtor.ledger_id,
      postings: [
        { ledger_id: debtor.ledger_id, dr_cr: "DR", amount: "11800.0000" },
        { ledger_id: sales.ledger_id, dr_cr: "CR", amount: "10000.0000" },
        { ledger_id: cgst.ledger_id, dr_cr: "CR", amount: "900.0000" },
        { ledger_id: sgst.ledger_id, dr_cr: "CR", amount: "900.0000" },
      ],
      gst: {
        supplier_state_code: "29",
        place_of_supply_state_code: "29",
        lines: [{ taxable_value: "10000.0000", gst_rate_pct: "18" }],
        cgst_ledger_id: cgst.ledger_id,
        sgst_ledger_id: sgst.ledger_id,
        igst_ledger_id: igst.ledger_id,
      },
    });

    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    // Drill Day Book -> the row -> the alter voucher frame.
    fireEvent.keyDown(app, { key: "g", altKey: true });
    fireEvent.mouseDown(screen.getByText("Day Book"));
    fireEvent.keyDown(app, { key: "Enter" });
    await waitFor(() => expect(screen.getByText("INV/0001")).toBeTruthy());
    fireEvent.keyDown(app, { key: "Enter" });

    await waitFor(() =>
      expect(screen.getByText(/Sales Voucher \(Alteration\)/)).toBeTruthy(),
    );
    // Hydration happened via GET (not the day book): the place of supply seeded.
    await waitFor(() => expect(spies.getVoucher).toHaveBeenCalled());
    await waitFor(() =>
      expect((screen.getByTestId("gst-pos") as HTMLInputElement).value).toBe("29"),
    );
    expect((screen.getByTestId("gst-taxable") as HTMLInputElement).value).toBe(
      "10000.0000",
    );

    // Re-derive at the SAME base/rate and accept -> alter PUT carries the loaded
    // id and REAL posting uuids (no ledger names).
    fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() => expect(spies.alterSalesVoucher).toHaveBeenCalledTimes(1));
    const [, voucherID, req] = spies.alterSalesVoucher!.mock.calls[0]!;
    expect(voucherID).toBe(posted.voucher_id);
    const postings = (req as { postings: PostingRequest[] }).postings;
    const ids = postings.map((p) => p.ledger_id);
    // CRITICAL: no posting ledger_id is a ledger NAME — they are all real uuids
    // (the name-as-uuid fix). Names would be e.g. "Acme Corp" / "Domestic Sales".
    const names = new Set([
      "Acme Corp",
      "Domestic Sales",
      "Output CGST",
      "Output SGST",
      "Output IGST",
    ]);
    for (const lid of ids) {
      expect(lid).toBeTruthy();
      expect(names.has(lid)).toBe(false);
      expect(lid.startsWith("ledger-")).toBe(true);
    }
    // The party Dr line carries the REAL hydrated debtor uuid.
    expect(ids[0]).toBe(debtor.ledger_id);
    // The carried-forward number is unchanged.
    expect((req as { voucher_no?: string }).voucher_no).toBe("INV/0001");
  });
});
