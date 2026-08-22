import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";
import { App } from "../App";
import { createMockClient } from "../api";
import type { CoredClient, GSTRequest, PostingRequest, VoucherResponse } from "../api";
import {
  _resetOutputTaxLedgerCache,
  _resetLedgerCache,
} from "./vouchers/gstVoucher";

/**
 * WS8a END-TO-END (in-browser): company.state_code flows
 * create -> persist -> HYDRATE -> voucher post.
 *
 * This is the test the "29" bootstrap constant made impossible. A Maharashtra
 * (state 27) company selling to a Maharashtra party is an INTRA-state supply and
 * must derive CGST + SGST. With the old hardcoded home state of "29" the client
 * classified the same sale as inter-state and built an IGST posting — which the
 * server would then reject (it re-derives from company.state_code and verifies
 * every posted tax leg), so the bug was a hard failure for every non-Karnataka
 * tenant, not a cosmetic one.
 *
 * Nothing here passes a home state into the component tree: the ONLY source is
 * the company master read back through the client.
 */

afterEach(cleanup);
beforeEach(() => {
  _resetOutputTaxLedgerCache();
  _resetLedgerCache();
  try {
    globalThis.localStorage?.clear();
  } catch {
    // jsdom always provides localStorage.
  }
});

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

/** Mint a company with the given home state and mark it as the persisted one. */
async function seedPersistedCompany(
  client: CoredClient,
  id: string,
  stateCode: string,
): Promise<void> {
  await client.createCompany({
    id,
    name: "Bombay Traders",
    fy_start_date: "2026-04-01",
    base_currency: "INR",
    state_code: stateCode,
  });
  // The party picker reads the company's REAL chart of accounts (it used to
  // offer a hardcoded sample list), so the party a test picks has to be an
  // account this company actually has — as it would be for a real user.
  await client.createLedger(id, {
    name: "Bharat Stores",
    group_name: "Sundry Debtors",
    state_code: "27",
  });
  globalThis.localStorage?.setItem("tally.companyId", id);
}

/** Drive F8 -> pick party -> fill taxable/rate -> Ctrl+A. */
async function postGstSale(party: string): Promise<void> {
  const app = screen.getByRole("application");
  fireEvent.keyDown(app, { key: "F8" });
  // Wait for the real voucher screen (not the "Preparing company…" placeholder):
  // the company must be HYDRATED before a GST post can be built.
  await waitFor(() => expect(screen.getByTestId("gst-taxable")).toBeTruthy());
  fireEvent.mouseDown(await screen.findByText(party));
  fireEvent.change(screen.getByTestId("gst-taxable"), { target: { value: "10000" } });
  fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });
  // ⛔ THE HSN IS NOW MANDATORY TO SAVE A SALES INVOICE (D60) — it is a Rule
  // 46(g) particular, and without it the invoice saved and then could not be
  // PRINTED. Keying it makes every assertion below the CONTROL for that new
  // refusal: an invoice that DOES carry an HSN still saves, unchanged.
  fireEvent.change(screen.getByTestId("gst-hsn"), { target: { value: "7326" } });
  fireEvent.keyDown(app, { key: "a", ctrlKey: true });
}

describe("home state code end-to-end (WS8a)", () => {
  it("a state-27 company selling to a state-27 party derives CGST+SGST, not IGST", async () => {
    const { client, spies } = spyClient(createMockClient({ seed: false }));
    await seedPersistedCompany(client, "co-mh", "27");

    render(<App client={client} />);
    // The home state came off the wire, from the company master.
    await waitFor(() => expect(spies.getCompany).toHaveBeenCalledWith("co-mh"));

    // "Bharat Stores" is the state-27 party in the picker -> place of supply 27.
    await postGstSale("Bharat Stores");

    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const [companyID, req] = spies.postSalesVoucher!.mock.calls[0]!;
    expect(companyID).toBe("co-mh");

    const gst = (req as { gst: GSTRequest }).gst;
    expect(gst.supplier_state_code).toBe("27");
    expect(gst.place_of_supply_state_code).toBe("27");

    // INTRA-state postings: party Dr 11800, income Cr 10000, CGST 900, SGST 900.
    const postings = (req as { postings: PostingRequest[] }).postings;
    expect(postings).toHaveLength(4);
    const amounts = postings.map((p) => p.amount);
    expect(amounts).toContain("11800.0000");
    expect(amounts.filter((a) => a === "900.0000")).toHaveLength(2);
    expect(postings.some((p) => p.ledger_id === gst.igst_ledger_id && p.amount !== "0.0000"))
      .toBe(false);

    // ...and the response the client receives carries the same split. CAVEAT
    // (do not overclaim): the mock's deriveGst calls the SAME deriveGstSplit
    // helper that buildGstVoucherRequest uses, so this pair of assertions is
    // NOT an independent oracle — it only proves the response echo is wired to
    // the company's state. The independent oracles for this workstream are
    // (a) the hand-computed literals above (11800 / 900+900 / 1800) and
    // (b) scripts/e2e-state-code.mts, where the split comes from the real Go
    // engine over HTTP.
    const res = (await spies.postSalesVoucher!.mock.results[0]!.value) as VoucherResponse;
    expect(res.cgst).toBe("900.0000");
    expect(res.sgst).toBe("900.0000");
    expect(res.igst).toBe("0.0000");
  });

  it("the SAME sale from a state-29 company is inter-state (IGST only)", async () => {
    // Control case proving the assertion above tracks company.state_code and not
    // something incidental about the party or the form.
    const { client, spies } = spyClient(createMockClient({ seed: false }));
    await seedPersistedCompany(client, "co-ka", "29");

    render(<App client={client} />);
    await waitFor(() => expect(spies.getCompany).toHaveBeenCalledWith("co-ka"));
    await postGstSale("Bharat Stores");

    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const [, req] = spies.postSalesVoucher!.mock.calls[0]!;
    const gst = (req as { gst: GSTRequest }).gst;
    expect(gst.supplier_state_code).toBe("29");
    const postings = (req as { postings: PostingRequest[] }).postings;
    expect(postings).toHaveLength(3);
    expect(postings.map((p) => p.amount)).toContain("1800.0000");

    const res = (await spies.postSalesVoucher!.mock.results[0]!.value) as VoucherResponse;
    expect(res.igst).toBe("1800.0000");
    expect(res.cgst).toBe("0.0000");
    expect(res.sgst).toBe("0.0000");
  });

  it("the mock mirrors the server's company.state_code default when the field is omitted", async () => {
    // Contract check on the oracle itself: cored fills an EMPTY supplier state
    // from company.state_code inside the post transaction. A mock that skipped
    // that step would derive IGST for a draft-accept body (which omits the
    // field) and would silently disagree with the real server.
    const client = createMockClient({ seed: false });
    await client.createCompany({
      id: "co-mh2",
      name: "Bombay Traders",
      fy_start_date: "2026-04-01",
      base_currency: "INR",
      state_code: "27",
    });
    const res = await client.postSalesVoucher("co-mh2", {
      voucher_date: "2026-05-01",
      postings: [
        { ledger_id: "party", dr_cr: "DR", amount: "11800.0000" },
        { ledger_id: "income", dr_cr: "CR", amount: "10000.0000" },
      ],
      gst: {
        // supplier_state_code deliberately ABSENT.
        place_of_supply_state_code: "27",
        lines: [{ taxable_value: "10000.0000", gst_rate_pct: "18" }],
      },
    });
    expect(res.cgst).toBe("900.0000");
    expect(res.sgst).toBe("900.0000");
    expect(res.igst).toBe("0.0000");
  });

  it("round-trips the full company master through getCompany", async () => {
    const client = createMockClient({ seed: false });
    await client.createCompany({
      id: "co-rt",
      name: "Round Trip Co",
      fy_start_date: "2027-04-01",
      base_currency: "INR",
      state_code: "07",
      gstin: "07AABCT1234M1Z5",
    });
    expect(await client.getCompany("co-rt")).toEqual({
      id: "co-rt",
      name: "Round Trip Co",
      fy_start_date: "2027-04-01",
      base_currency: "INR",
      state_code: "07",
      gstin: "07AABCT1234M1Z5",
      // Migration-0015 profile. None was supplied to createCompany above, so
      // each lands on its documented default -- and books_beginning_from
      // tracks fy_start_date rather than a hardcoded date.
      mailing_name: "",
      address: "",
      city: "",
      pincode: "",
      country: "India",
      books_beginning_from: "2027-04-01",
      currency_symbol: "₹",
      currency_formal_name: "Indian Rupees",
      decimal_places: 2,
      // The inventory escape hatch (D5/D43). FALSE is the column default and
      // the restrictive answer; an omitted key must land here rather than
      // opening the hatch.
      allow_negative_stock: false,
    });
  });
});
