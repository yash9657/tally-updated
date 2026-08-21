import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";

import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient } from "../api";
import type { CoredClient, VoucherRequest } from "../api";
import { _resetOutputTaxLedgerCache, _resetLedgerCache } from "./vouchers/gstVoucher";

afterEach(cleanup);
beforeEach(() => {
  _resetOutputTaxLedgerCache();
  _resetLedgerCache();
});

/**
 * ===========================================================================
 * ⛔ REGISTER ROW D3, COLLECTION HALF — A TCS COLLECTION COULD NOT BE KEYED,
 * AND THE REASON WAS NOT THE BLOCK. IT WAS THE MISSING LINE.
 * ===========================================================================
 *
 * `statutoryTax` was `null` on Sales, and the comment at the field said why: a
 * collection has to be CHARGED to the buyer, and the GST invoice form
 * SYNTHESISES its postings rather than taking a Dr/Cr grid, so there was no line
 * for the operator to name. A `tcs` block on its own records the collection for
 * the return and POSTS NOTHING — the invoice would charge the goods and the GST
 * while the return says a further 1% was collected, which is money the company
 * must deposit and never billed.
 *
 * ⛔ THE FIX IS AN ADDITIONAL-CHARGE LINE THE OPERATOR KEYS, and that was a
 * DECISION. The alternative — deriving the line inside `buildGstVoucherRequest`
 * from the collection — was put to the user and rejected: it makes the browser
 * the author of an amount the server owns. The server derives the collection
 * from its own rate table and refuses the voucher when the posted leg disagrees.
 *
 * ⚠️ EVERY TEST HERE DRIVES THE REAL SHELL AND PRESSES REAL KEYS, for the reason
 * the TDS suite next door states: the failure mode WAS being unreachable, and a
 * test that called `postSalesVoucher` directly would pass against the broken
 * build.
 */

function spyClient(): {
  client: CoredClient;
  posted: () => VoucherRequest | undefined;
  created: () => string[];
} {
  const base = createMockClient({ seed: false });
  const spies: Record<string, ReturnType<typeof vi.fn>> = {};
  const client = {} as Record<string, unknown>;
  for (const key of Object.keys(base) as (keyof CoredClient)[]) {
    const orig = base[key] as unknown as (...a: unknown[]) => unknown;
    const fn = vi.fn((...args: unknown[]) => orig.apply(base, args));
    spies[key] = fn;
    client[key] = fn;
  }
  return {
    client: client as unknown as CoredClient,
    posted: () => {
      const call = spies["postSalesVoucher"]?.mock.calls[0];
      return call ? (call[1] as VoucherRequest) : undefined;
    },
    /** Every ledger name this run CREATED — the D39/D81 oracle. */
    created: () =>
      (spies["createLedger"]?.mock.calls ?? []).map(
        (c) => (c[1] as { name: string }).name,
      ),
  };
}

/**
 * ⛔ THE BUYER MUST EXIST, for the reason the deductee must in the TDS suite:
 * the panel resolves the ledger id from the chart and REFUSES a name it cannot
 * find rather than minting a party to satisfy a 422.
 */
async function seedBuyer(client: CoredClient) {
  await client.createLedger("co-1", {
    name: "Vikram Scrap Traders",
    group_name: "Sundry Debtors",
    state_code: "29",
  });
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

/** F8 opens the ACCOUNTING invoice — the mode that takes a charge line. */
async function openAccountingSale() {
  fireEvent.keyDown(screen.getByRole("application"), { key: "F8" });
  return await screen.findByTestId("gst-panel");
}

/** The item grid is one Ctrl+H away, and it carries no charge line. */
async function openItemSale() {
  fireEvent.keyDown(screen.getByRole("application"), { key: "F8" });
  await screen.findByTestId("gst-panel");
  fireEvent.keyDown(screen.getByRole("application"), { key: "h", ctrlKey: true });
  fireEvent.mouseDown(await screen.findByText("Item Invoice"));
  return await screen.findByTestId("item-invoice-panel");
}

/** A 100000 scrap sale at 18% to a buyer in the home state. */
async function keyTheSale() {
  // ⛔ THE PARTY IS PICKED FROM THE COMPANY'S OWN CHART, not typed — a typed
  // name binds no uuid, and the TCS block resolves the buyer id from the same
  // chart. Picking it is also what proves the picker read the ledger the test
  // seeded.
  fireEvent.mouseDown(await screen.findByText("Vikram Scrap Traders"));
  fireEvent.change(screen.getByTestId("gst-taxable"), { target: { value: "100000" } });
  fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });
  fireEvent.change(screen.getByTestId("gst-hsn"), { target: { value: "7204" } });
}

function keyTheCharge(ledger = "TCS Payable", amount = "2000") {
  fireEvent.change(screen.getByTestId("charge-ledger:0"), { target: { value: ledger } });
  fireEvent.change(screen.getByTestId("charge-amount:0"), { target: { value: amount } });
}

async function keyTheCollection(over: { section?: string; leg?: string } = {}) {
  fireEvent.click(await screen.findByTestId("tax-tcs-open"));
  await screen.findByTestId("tax-tcs-panel");
  await waitFor(() =>
    expect(
      screen.getByTestId("tax-tcs-section").querySelectorAll("option").length,
    ).toBeGreaterThan(1),
  );
  fireEvent.change(screen.getByTestId("tax-tcs-section"), {
    target: { value: over.section ?? "206C(1)-SCRAP" },
  });
  fireEvent.change(screen.getByTestId("tax-tcs-party"), {
    target: { value: "Vikram Scrap Traders" },
  });
  fireEvent.change(screen.getByTestId("tax-tcs-amount"), { target: { value: "100000" } });
  if (over.leg !== "") {
    fireEvent.change(screen.getByTestId("tax-tcs-leg"), {
      target: { value: over.leg ?? "TCS Payable" },
    });
  }
}

const accept = () =>
  fireEvent.keyDown(screen.getByRole("application"), { key: "a", ctrlKey: true });

describe("⛔ D3 (TCS): the block is REACHABLE — and it is a COLLECTION, not a deduction", () => {
  it("the accounting-mode Sales invoice offers a TCS block, collapsed until asked for", async () => {
    renderShell(spyClient().client);
    await openAccountingSale();
    expect(await screen.findByTestId("tax-tcs-open")).toBeTruthy();
    expect(screen.queryByTestId("tax-tcs-panel")).toBeNull();
    // ⛔ AND NOT A DEDUCTION. A seller collects from its buyer; it does not
    // withhold from itself. `primarySide` reads "CR" here AND on the credit
    // note, which is exactly why the kind is stated per class.
    expect(screen.queryByTestId("tax-tds-open")).toBeNull();
  });

  it("⛔ the ITEM invoice offers none, and SAYS WHY rather than being silently blank", async () => {
    renderShell(spyClient().client);
    await openItemSale();
    expect(screen.queryByTestId("tax-tcs-open")).toBeNull();
    const said = (await screen.findByTestId("item-statutory-unavailable")).textContent ?? "";
    // The absence must name the remedy: a silent one reads as "this build has
    // no TCS", and it has one, one Ctrl+H away.
    expect(said).toMatch(/generated by the server/i);
    expect(said).toMatch(/Accounting Invoice/);
  });

  it("⛔ a PURCHASE offers none — a buyer does not collect tax from its own supplier", async () => {
    renderShell(spyClient().client);
    fireEvent.keyDown(screen.getByRole("application"), { key: "F9" });
    await screen.findByTestId("gst-panel");
    expect(screen.queryByTestId("tax-tcs-open")).toBeNull();
    expect(screen.queryByTestId("tax-tds-open")).toBeNull();
  });

  it("⛔ the leg picker reads the CHARGE LINES, not the chart of accounts", async () => {
    const { client } = spyClient();
    await seedBuyer(client);
    renderShell(client);
    await openAccountingSale();
    await keyTheSale();
    fireEvent.click(await screen.findByTestId("tax-tcs-open"));
    await screen.findByTestId("tax-tcs-panel");
    // Nothing charged yet: the picker can offer nothing, because a leg that is
    // not posted is a verification that never runs.
    let legs = screen.getByTestId("tax-tcs-leg") as HTMLSelectElement;
    expect([...legs.querySelectorAll("option")].map((o) => o.value)).toEqual([""]);
    // The buyer IS in the chart and must NOT be offered — it is not a charge.
    expect(legs.textContent).not.toMatch(/Vikram/);
    keyTheCharge();
    legs = screen.getByTestId("tax-tcs-leg") as HTMLSelectElement;
    expect(
      [...legs.querySelectorAll("option")].map((o) => o.value),
    ).toEqual(["", "TCS Payable"]);
  });
});

describe("⛔ D3 (TCS): what reaches the wire", () => {
  it("the charge is CHARGED to the buyer and the tcs block names the line that carries it", async () => {
    const { client, posted } = spyClient();
    await seedBuyer(client);
    renderShell(client);
    await openAccountingSale();
    await keyTheSale();
    keyTheCharge();
    await keyTheCollection();
    accept();

    await waitFor(() => expect(posted()).toBeDefined());
    const req = posted()!;
    // ⛔ THE BUYER REALLY OWES IT. 100000 + 18000 GST + 2000 charge.
    const party = req.postings!.find((p) => p.ledger_id === req.party_ledger_id)!;
    expect(party.dr_cr).toBe("DR");
    expect(party.amount).toBe("120000.0000");
    // ⛔ AND IT IS **NOT** IN THE TAXABLE VALUE. Folding it in would raise the
    // GST on a charge that is not a supply — and the server would agree with
    // the client about a figure both had wrong.
    expect(req.gst!.lines![0]!.taxable_value).toBe("100000.0000");
    // The charge is its own CREDIT leg.
    const charge = req.postings!.find((p) => p.amount === "2000.0000")!;
    expect(charge.dr_cr).toBe("CR");
    // ⛔ AND THE BLOCK NAMES THAT EXACT POSTING. An id matching no posting makes
    // the server's verification sum zero — a check that silently does not run.
    expect(req.tcs).toBeDefined();
    expect(req.tcs!.section).toBe("206C(1)-SCRAP");
    expect(req.tcs!.taxable_amount).toBe("100000.0000");
    expect(req.tcs!.tcs_ledger_id).toBe(charge.ledger_id);
    expect(req.tcs!.buyer_ledger_id).toBe(req.party_ledger_id);
    // ⛔ AND NO `tds` — the two blocks are not interchangeable and the server
    // would derive a withholding from a sale.
    expect(req.tds).toBeUndefined();
  });

  it("a charge with NO collection still posts — freight is not a statutory anything", async () => {
    const { client, posted } = spyClient();
    await seedBuyer(client);
    renderShell(client);
    await openAccountingSale();
    await keyTheSale();
    keyTheCharge("Freight Recovered", "500");
    accept();
    await waitFor(() => expect(posted()).toBeDefined());
    const req = posted()!;
    expect(req.tcs).toBeUndefined();
    const party = req.postings!.find((p) => p.ledger_id === req.party_ledger_id)!;
    expect(party.amount).toBe("118500.0000");
  });

  it("no charge and no collection posts exactly what it always did", async () => {
    const { client, posted } = spyClient();
    await seedBuyer(client);
    renderShell(client);
    await openAccountingSale();
    await keyTheSale();
    accept();
    await waitFor(() => expect(posted()).toBeDefined());
    const req = posted()!;
    expect(req.tcs).toBeUndefined();
    expect(req.postings!).toHaveLength(4); // party + sales + CGST + SGST
    const party = req.postings!.find((p) => p.ledger_id === req.party_ledger_id)!;
    expect(party.amount).toBe("118000.0000");
  });
});

describe("⛔ D3 (TCS): a half-keyed invoice is refused BEFORE any account is created", () => {
  it("a charge with an amount and NO ledger is refused, and NOTHING is minted", async () => {
    const { client, posted, created } = spyClient();
    await seedBuyer(client);
    renderShell(client);
    await openAccountingSale();
    await keyTheSale();
    fireEvent.change(screen.getByTestId("charge-amount:0"), { target: { value: "2000" } });
    const before = created().length;
    accept();
    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toMatch(/names no ledger/i),
    );
    expect(posted()).toBeUndefined();
    // ⛔ D39/D81: the refusal must come BEFORE the resolvers. Three tax heads,
    // the base account and the charge's own ledger would otherwise be left
    // behind on a voucher that never posted. Measured as a DELTA, because the
    // test's own seed created the buyer through this same spied client.
    expect(created().slice(before)).toEqual([]);
  });

  it("a charge with a ledger and no amount is refused", async () => {
    const { client, posted } = spyClient();
    await seedBuyer(client);
    renderShell(client);
    await openAccountingSale();
    await keyTheSale();
    fireEvent.change(screen.getByTestId("charge-ledger:0"), { target: { value: "TCS Payable" } });
    accept();
    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toMatch(/is empty/i),
    );
    expect(posted()).toBeUndefined();
  });

  it("⛔ a named tax leg that is NOT a charge line on this invoice is REFUSED", async () => {
    const { client, posted, created } = spyClient();
    await seedBuyer(client);
    renderShell(client);
    await openAccountingSale();
    await keyTheSale();
    keyTheCharge("Freight Recovered", "500");
    // The collection says it is carried by a line that is not on the invoice.
    await keyTheCollection({ leg: "" });
    fireEvent.change(screen.getByTestId("tax-tcs-leg"), { target: { value: "" } });
    // Force the refusal through the FORM's own state rather than the picker,
    // which can only offer real lines — this is the hydration/stale case.
    const refusal = screen.queryByTestId("tax-tcs-refusal");
    // With no leg named the state is legal and DISCLOSED, not refused.
    expect(refusal).toBeNull();
    expect(
      (await screen.findByTestId("tax-tcs-unposted")).textContent ?? "",
    ).toMatch(/recorded for the return/i);
    accept();
    await waitFor(() => expect(posted()).toBeDefined());
    // It posted, and it posted WITHOUT a leg id — the disclosure was the truth.
    expect(posted()!.tcs!.tcs_ledger_id).toBeUndefined();
    expect(created()).toContain("Freight Recovered");
  });
});

/**
 * ===========================================================================
 * ⛔⛔ THE ALTER ROUND TRIP — REGISTER ROW D88's SHAPE, ONE FIELD OVER.
 * ===========================================================================
 * An ALTER RE-STATES a voucher: whatever the body carries becomes the current
 * version and whatever it omits is simply not written. So a form that cannot
 * re-state a charge DELETES it — the buyer's total silently falls by the charge
 * and, on a TCS invoice, the collected tax stops being posted while the return
 * still says it was collected.
 *
 * ⛔ AND THE CHARGE SITS ON THE SAME SIDE AS THE SALE, which is what made this
 * more than a copy of the rate fix: the old one-credit rule that found the base
 * leg stopped matching the moment a second credit existed, so the alter ALSO
 * re-pointed the goods to the canonical `Domestic Sales`. Both failures balance.
 */
async function drill(app: HTMLElement, voucherNo: string) {
  fireEvent.keyDown(app, { key: "Escape" });
  fireEvent.keyDown(app, { key: "g", altKey: true });
  fireEvent.mouseDown(screen.getByText("Day Book"));
  fireEvent.keyDown(app, { key: "Enter" });
  await waitFor(() => expect(screen.getByText(voucherNo)).toBeTruthy());
  fireEvent.keyDown(app, { key: "Enter" });
}

describe("⛔ D3/D88: an alter RE-STATES the charge instead of deleting it", () => {
  it("the charge comes back on the form, and the re-save still bills the party for it", async () => {
    const base = createMockClient({ seed: false });
    await seedBuyer(base);
    const spies: Record<string, ReturnType<typeof vi.fn>> = {};
    const client = {} as Record<string, unknown>;
    for (const key of Object.keys(base) as (keyof CoredClient)[]) {
      const orig = base[key] as unknown as (...a: unknown[]) => unknown;
      spies[key] = vi.fn((...args: unknown[]) => orig.apply(base, args));
      client[key] = spies[key];
    }
    renderShell(client as unknown as CoredClient);
    const app = screen.getByRole("application");

    await openAccountingSale();
    await keyTheSale();
    keyTheCharge();
    await keyTheCollection();
    accept();
    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const posted = await spies.postSalesVoucher!.mock.results[0]!.value;

    await drill(app, posted.voucher_no);
    await waitFor(() => expect(spies.getVoucher).toHaveBeenCalled());

    // ⛔ VISIBLE ON THE FORM, which is the assertion a build without
    // gstLegsFromView fails: the charge line comes back empty and the operator
    // has no way to know the next Ctrl+A drops 2000 off the invoice.
    await waitFor(() =>
      expect((screen.getByTestId("charge-ledger:0") as HTMLInputElement).value).toBe(
        "TCS Payable",
      ),
    );
    expect((screen.getByTestId("charge-amount:0") as HTMLInputElement).value).toBe(
      "2000.0000",
    );

    // The ordinary round trip: change nothing, save.
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.alterSalesVoucher).toHaveBeenCalledTimes(1));
    const req = spies.alterSalesVoucher!.mock.calls[0]![2] as VoucherRequest;
    const party = req.postings!.find((p) => p.dr_cr === "DR")!;
    expect(party.amount).toBe("120000.0000");
    expect(req.postings!.filter((p) => p.amount === "2000.0000")).toHaveLength(1);
    // ⛔ AND THE GOODS ARE STILL THE GOODS. The base leg must not have been
    // re-pointed: 100000 credited, once.
    expect(req.postings!.filter((p) => p.amount === "100000.0000")).toHaveLength(1);
    expect(req.gst!.lines![0]!.taxable_value).toBe("100000.0000");
  });
});

/**
 * ===========================================================================
 * ⛔⛔ THE DEFECT DRIVING FOUND AND THE UNIT SUITE DID NOT.
 * ===========================================================================
 * Every test above uses a party WITHOUT bill-by-bill, so the bill-reference
 * panel never renders and nothing computes "what the party owes" twice in the
 * same breath. On a party that DOES maintain it, the panel's auto-tracking
 * default reference was built on the pre-charge gross while the party leg
 * carried the charge — so a real invoice was certified "referenced in full
 * (118000.0000)" on screen and then refused by the server:
 *
 *   ledger: bill references on this ledger total 118000.0000 but it posts
 *   120000.0000 — the difference is 2000.0000
 *
 * with no way for the operator to see why. Seen in a browser on 2026-08-21.
 */
describe("⛔ a bill-by-bill party: the reference tracks the CHARGE too", () => {
  it("the panel's own caption and its default reference both carry the charge", async () => {
    const base = createMockClient({ seed: false });
    await base.createLedger("co-1", {
      name: "Bill By Bill Buyer",
      group_name: "Sundry Debtors",
      state_code: "29",
      // ⛔ THE WHOLE POINT OF THIS FIXTURE. Without it the panel never renders
      // and the test is the one above with a longer name.
      maintain_bill_by_bill: true,
    });
    const spies: Record<string, ReturnType<typeof vi.fn>> = {};
    const client = {} as Record<string, unknown>;
    for (const key of Object.keys(base) as (keyof CoredClient)[]) {
      const orig = base[key] as unknown as (...a: unknown[]) => unknown;
      spies[key] = vi.fn((...args: unknown[]) => orig.apply(base, args));
      client[key] = spies[key];
    }
    renderShell(client as unknown as CoredClient);
    await openAccountingSale();
    fireEvent.mouseDown(await screen.findByText("Bill By Bill Buyer"));
    fireEvent.change(screen.getByTestId("gst-taxable"), { target: { value: "100000" } });
    fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });
    fireEvent.change(screen.getByTestId("gst-hsn"), { target: { value: "7204" } });
    await screen.findByTestId("bill-refs-panel");
    keyTheCharge();
    await waitFor(() =>
      // The caption names the leg the server will check against. 118000 here is
      // the defect, verbatim.
      expect(screen.getByTestId("bill-refs-panel").textContent).toMatch(/120000\.0000/),
    );
    expect(screen.getByTestId("bill-refs-panel").textContent).not.toMatch(/118000\.0000/);

    accept();
    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const req = spies.postSalesVoucher!.mock.calls[0]![1] as VoucherRequest;
    // ⛔ THE INVARIANT THE SERVER CHECKS TO THE PAISA: the reference totals to
    // the party posting. Asserted as an EQUALITY between the two, not against a
    // literal, so it stays true if the fixture's figures ever change.
    const party = req.postings!.find((p) => p.ledger_id === req.party_ledger_id)!;
    const referenced = req.bill_allocations!.reduce(
      (acc, b) => acc + Number(b.amount),
      0,
    );
    expect(referenced.toFixed(4)).toBe(Number(party.amount).toFixed(4));
    expect(party.amount).toBe("120000.0000");
  });
});
