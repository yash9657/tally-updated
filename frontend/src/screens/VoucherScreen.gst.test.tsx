import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";
import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient, ApiError } from "../api";
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

/**
 * Give a company the party ledgers a test then PICKS.
 *
 * The picker used to offer a hardcoded sample list, so a test could select
 * "Acme Traders" from a company that had no such account. It now reads the real
 * chart of accounts, so a test has to set the books up the way a real company
 * is — which is the more faithful arrangement anyway: the accounts a user picks
 * from are the accounts that exist.
 */
async function seedParties(base: CoredClient): Promise<void> {
  for (const [name, state] of [
    ["Acme Traders", "29"], // same state as the home company -> intra-state
    ["Bharat Stores", "27"], // different state -> inter-state
  ] as const) {
    await base.createLedger("co-1", {
      name,
      group_name: "Sundry Debtors",
      state_code: state,
    });
  }
}

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
    const base = createMockClient({ seed: false });
    await seedParties(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" }); // sales voucher (create)

    // Choose a party (state 29 -> intra-state vs home 29) and a taxable base+rate.
    // The option comes from the company's OWN chart of accounts now, so this
    // also proves the picker actually read it.
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
    const base = createMockClient({ seed: false });
    await seedParties(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" });

    fireEvent.mouseDown(await screen.findByText("Bharat Stores")); // state 27 vs home 29
    fireEvent.change(screen.getByTestId("gst-taxable"), {
      target: { value: "10000" },
    });
    fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });
    // ⛔ THE HSN IS NOW MANDATORY TO SAVE A SALES INVOICE (D60) — it is a Rule
    // 46(g) particular, and without it the invoice saved and then could not be
    // PRINTED. Keying it makes every assertion below the CONTROL for that new
    // refusal: an invoice that DOES carry an HSN still saves, unchanged.
    fireEvent.change(screen.getByTestId("gst-hsn"), { target: { value: "7326" } });
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
    // ⛔ THE HSN IS NOW MANDATORY TO SAVE A SALES INVOICE (D60) — it is a Rule
    // 46(g) particular, and without it the invoice saved and then could not be
    // PRINTED. Keying it makes every assertion below the CONTROL for that new
    // refusal: an invoice that DOES carry an HSN still saves, unchanged.
    fireEvent.change(screen.getByTestId("gst-hsn"), { target: { value: "7326" } });
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

/**
 * ⛔⛔ AN ALTER MAY NOT MOVE THE VOUCHER'S INCOME / EXPENSE HEAD.
 *
 * FOUND BY DRIVING D41 IN A BROWSER, and it is data loss rather than an orphan
 * master. `buildGstRequest` always ensured the ORIENTATION'S CANONICAL base
 * ("Domestic Sales" / "Purchases"), so opening any accounting-mode GST voucher
 * booked to a different head and pressing Ctrl+A silently re-pointed it. On the
 * demo books, with nothing edited:
 *
 *     version 1:  Dr Raw Material Purchases  30000.0000
 *     version 2:  Dr Purchases               30000.0000
 *
 * A "Purchases" ledger was created to receive it, the voucher still balanced,
 * the tax was unchanged — and the company's expense analysis had changed
 * because somebody LOOKED at a voucher. No totals check anywhere can see it.
 */
describe("⛔ an alter preserves the head the voucher is booked to", () => {
  const seedPurchase = async (baseName: string) => {
    const base = createMockClient({ seed: false });
    const creditor = await base.createLedger("co-1", {
      name: "Steel Suppliers",
      group_name: "Sundry Creditors",
      state_code: "29",
    });
    const head = await base.createLedger("co-1", {
      name: baseName,
      group_name: "Purchase Accounts",
    });
    const cgst = await base.createLedger("co-1", { name: "Input CGST", group_name: "Duties & Taxes" });
    const sgst = await base.createLedger("co-1", { name: "Input SGST", group_name: "Duties & Taxes" });
    const igst = await base.createLedger("co-1", { name: "Input IGST", group_name: "Duties & Taxes" });
    const posted = await base.postPurchaseVoucher("co-1", {
      voucher_no: "PUR/0001",
      voucher_date: "2025-05-10",
      party: "Steel Suppliers",
      party_ledger_id: creditor.ledger_id,
      postings: [
        { ledger_id: creditor.ledger_id, dr_cr: "CR", amount: "11800.0000" },
        { ledger_id: head.ledger_id, dr_cr: "DR", amount: "10000.0000" },
        { ledger_id: cgst.ledger_id, dr_cr: "DR", amount: "900.0000" },
        { ledger_id: sgst.ledger_id, dr_cr: "DR", amount: "900.0000" },
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
    return { base, posted, head, creditor };
  };

  const drillAndAccept = async (client: CoredClient) => {
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "g", altKey: true });
    fireEvent.mouseDown(screen.getByText("Day Book"));
    fireEvent.keyDown(app, { key: "Enter" });
    await waitFor(() => expect(screen.getByText("PUR/0001")).toBeTruthy());
    fireEvent.keyDown(app, { key: "Enter" });
    await waitFor(() =>
      expect(screen.getByText(/Purchase Voucher \(Alteration\)/)).toBeTruthy(),
    );
    fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    return app;
  };

  it("keeps a NON-canonical expense head on the altered voucher", async () => {
    const { base, posted, head } = await seedPurchase("Raw Material Purchases");
    const { client, spies } = spyClient(base);
    await drillAndAccept(client);

    await waitFor(() => expect(spies.alterPurchaseVoucher).toHaveBeenCalledTimes(1));
    const [, voucherID, req] = spies.alterPurchaseVoucher!.mock.calls[0]!;
    expect(voucherID).toBe(posted.voucher_id);
    const postings = (req as { postings: PostingRequest[] }).postings;
    const debits = postings.filter((p) => p.dr_cr === "DR").map((p) => p.ledger_id);
    // ⛔ THE HEAD IS STILL THE ONE THE VOUCHER WAS BOOKED TO.
    expect(debits).toContain(head.ledger_id);
  });

  it("⛔ and mints NO 'Purchases' ledger on the way", async () => {
    const { base } = await seedPurchase("Raw Material Purchases");
    const before = (await base.listLedgers("co-1")).ledgers.length;
    const { client } = spyClient(base);
    await drillAndAccept(client);
    await waitFor(async () =>
      expect((await base.listLedgers("co-1")).ledgers.length).toBe(before),
    );
    const names = (await base.listLedgers("co-1")).ledgers.map((l) => l.name);
    expect(names).not.toContain("Purchases");
  });

  it("⛔ CONTROL: a voucher booked to the CANONICAL head still alters cleanly", async () => {
    // Without this control, "preserve whatever was hydrated" could be satisfied
    // by a builder that had stopped resolving a base ledger at all.
    const { base, head } = await seedPurchase("Purchases");
    const { client, spies } = spyClient(base);
    await drillAndAccept(client);
    await waitFor(() => expect(spies.alterPurchaseVoucher).toHaveBeenCalledTimes(1));
    const [, , req] = spies.alterPurchaseVoucher!.mock.calls[0]!;
    const debits = (req as { postings: PostingRequest[] }).postings
      .filter((p) => p.dr_cr === "DR")
      .map((p) => p.ledger_id);
    expect(debits).toContain(head.ledger_id);
  });
});

/**
 * The PARTY PICKER reads the company's real chart of accounts.
 *
 * It used to offer a hardcoded sample list ("Acme Traders", "Bharat Stores",
 * "Chandra & Co") that existed in no seeded company. Two things went wrong with
 * that, and these tests pin both fixes:
 *
 *  - the names offered were not accounts, so picking one CREATED a ledger as a
 *    side effect of keying a voucher — a silent way to mint a near-duplicate of
 *    a real party, and a 403 for a Data Entry operator who may key vouchers but
 *    may not create masters;
 *  - and a real company's actual customers were not offered at all.
 */
describe("the party picker reads the real chart of accounts", () => {
  it("offers the company's OWN party accounts, and not other groups", async () => {
    const base = createMockClient({ seed: false });
    await base.createLedger("co-1", {
      name: "Zenith Distributors",
      group_name: "Sundry Debtors",
      state_code: "07",
    });
    await base.createLedger("co-1", {
      name: "Kaveri Suppliers",
      group_name: "Sundry Creditors",
      state_code: "29",
    });
    // NOT a party: an income ledger must never be offered as one, or the entry
    // would be nonsensical (and, for a sale, unbalanced in a plausible way).
    await base.createLedger("co-1", {
      name: "Domestic Sales",
      group_name: "Sales Accounts",
    });

    const { client } = spyClient(base);
    renderShell(client);
    fireEvent.keyDown(screen.getByRole("application"), { key: "F8" });

    expect(await screen.findByText("Zenith Distributors")).toBeTruthy();
    expect(screen.getByText("Kaveri Suppliers")).toBeTruthy();
    expect(screen.queryByText("Domestic Sales")).toBeNull();
    // ...and the old hardcoded samples are gone for good.
    expect(screen.queryByText("Chandra & Co")).toBeNull();
  });

  it("picking an EXISTING party creates no ledger — it carries the real id", async () => {
    const base = createMockClient({ seed: false });
    const created = await base.createLedger("co-1", {
      name: "Zenith Distributors",
      group_name: "Sundry Debtors",
      state_code: "29",
    });
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" });

    fireEvent.mouseDown(await screen.findByText("Zenith Distributors"));
    fireEvent.change(screen.getByTestId("gst-taxable"), { target: { value: "10000" } });
    fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });
    // ⛔ THE HSN IS NOW MANDATORY TO SAVE A SALES INVOICE (D60) — it is a Rule
    // 46(g) particular, and without it the invoice saved and then could not be
    // PRINTED. Keying it makes every assertion below the CONTROL for that new
    // refusal: an invoice that DOES carry an HSN still saves, unchanged.
    fireEvent.change(screen.getByTestId("gst-hsn"), { target: { value: "7326" } });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const [, req] = spies.postSalesVoucher!.mock.calls[0]!;
    const postings = (req as { postings: PostingRequest[] }).postings;
    // The posting carries the ledger id that already existed.
    expect(postings.some((p) => p.ledger_id === created.ledger_id)).toBe(true);

    // And NO ledger was created for the party. (The tax/income ledgers are
    // still ensured, so this asserts the absence of the PARTY create, not the
    // absence of every create — a blanket "never creates" would be wrong.)
    const createdNames = spies.createLedger!.mock.calls.map(
      (c) => (c[1] as { name: string }).name,
    );
    expect(createdNames).not.toContain("Zenith Distributors");
  });

  it("explains a chart-of-accounts failure instead of showing an empty picker", async () => {
    // An empty list with no message reads as "this company has no customers",
    // which is a very different — and much more alarming — statement than "the
    // list could not be loaded".
    const base = createMockClient({ seed: false });
    const { client } = spyClient(base);
    const failing: CoredClient = {
      ...client,
      listLedgers: async () => {
        throw new ApiError(403, "default access: none", "forbidden");
      },
    };
    renderShell(failing);
    fireEvent.keyDown(screen.getByRole("application"), { key: "F8" });

    const err = await screen.findByTestId("party-list-error");
    expect(err.textContent).toContain("403");
    expect(err.textContent).toContain("type a name to create the party");
  });
});
