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

/**
 * ===========================================================================
 * ⛔⛔ A REFUSED Ctrl+A MUST NOT LEAVE A MASTER BEHIND — THE WHOLE CLASS.
 * ===========================================================================
 *
 * §7.1 D39 and D41 are ONE defect on three keystrokes, and both were DRIVEN in
 * a real browser against real cored:
 *
 *   D39 (HIGH) — Ctrl+A on a voucher whose cost split is INCOMPLETE on one axis
 *     created EVERY ledger the operator had named, and THEN the server refused
 *     the voucher. The chart of accounts gained accounts with no voucher behind
 *     them; the operator saw only the 422. Reproduced on BOTH grids.
 *   D41 (MEDIUM) — opening a Purchase from the Day Book and pressing Ctrl+A
 *     created a new "Purchases" ledger over the wire and then failed the write
 *     with "invalid gst line gst_rate_pct". A REFUSED ALTER LEFT A MASTER.
 *
 * ⛔ THE ORACLE HERE IS **`createLedger` WAS NEVER CALLED**, never the status
 * code and never the absence of a voucher. That is RULE 4 applied to masters: a
 * refused save looks identical from the voucher table whether or not it wrote
 * three accounts on the way, and the whole defect lives in that difference.
 *
 * ⚠️ EVERY TEST DRIVES THE REAL SHELL AND PRESSES REAL KEYS. The defect is a
 * property of the accept PATH; a test calling a builder directly would pass
 * against the broken build.
 *
 * ⚠️ AND EVERY REFUSAL CARRIES ITS CONTROL. A gate that refuses everything
 * satisfies the defect and destroys the feature, and the two are
 * indistinguishable from the refusing side.
 */

function spyClient(base: CoredClient): {
  client: CoredClient;
  base: CoredClient;
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
  return { client: client as unknown as CoredClient, base, spies };
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

/** The bank the money moves through, so only the PARTICULARS are new names. */
async function seedBank(base: CoredClient): Promise<void> {
  await base.createLedger("co-1", {
    name: "HDFC Bank",
    group_name: "Bank Accounts",
  });
}

/** The mock's fixed axes: Department / Branch, with their centres. */
async function firstAxis(
  base: CoredClient,
): Promise<{ axisID: string; centreID: string }> {
  const axes = (await base.listCostCategories("co-1")).cost_categories;
  const cat = axes.find((a) => a.name === "Department")!;
  const centre = (await base.listCostCentres("co-1")).cost_centres.find(
    (c) => c.cost_category_id === cat.id,
  )!;
  return { axisID: cat.id, centreID: centre.id };
}

/**
 * Every name this accept would have had to CREATE.
 *
 * ⚠️ Reported as names rather than as a count, so a failure says WHICH account
 * was minted for a voucher that was never written — "Office Rent" reads as the
 * defect; "1" reads as an off-by-one.
 */
function createdLedgerNames(spies: Record<string, ReturnType<typeof vi.fn>>): string[] {
  return spies.createLedger!.mock.calls.map(
    (c) => (c[1] as { name: string }).name,
  );
}

describe("⛔ the master gate: single entry (D39, DRIVEN)", () => {
  it("a cost axis short by any amount creates NO ledger and posts nothing", async () => {
    const base = createMockClient({ seed: false });
    await seedBank(base);
    const { axisID, centreID } = await firstAxis(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F5" }); // Payment
    await screen.findByTestId("single-entry-panel");
    fireEvent.mouseDown(await screen.findByText("HDFC Bank"));
    // ⛔ A LEDGER THIS COMPANY DOES NOT HAVE. That is the whole point: an
    // existing name resolves and creates nothing, so a test keyed against the
    // seeded chart could not see the defect at all.
    fireEvent.change(screen.getByTestId("single-ledger:0"), {
      target: { value: "Office Rent" },
    });
    fireEvent.change(screen.getByTestId("single-amount:0"), {
      target: { value: "2500" },
    });

    // Analyse it on the Department axis — but only for 1,000 of the 2,500.
    fireEvent.click(await screen.findByTestId("alloc:0-add"));
    fireEvent.change(screen.getByTestId("alloc:0-axis:0"), {
      target: { value: axisID },
    });
    fireEvent.change(screen.getByTestId("alloc:0-centre:0"), {
      target: { value: centreID },
    });
    fireEvent.change(screen.getByTestId("alloc:0-amount:0"), {
      target: { value: "1000" },
    });

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    // The operator is TOLD, and told which row and by how much.
    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toMatch(/short by 1500/),
    );
    expect(screen.getByRole("status").textContent).toMatch(/Office Rent/);

    // ⛔ THE ORACLE: not one account was minted for a voucher that was refused.
    expect(createdLedgerNames(spies)).toEqual([]);
    expect(spies.postPaymentVoucher).not.toHaveBeenCalled();
  });

  it("⛔ CONTROL: the SAME payment with the axis complete creates the ledger AND posts", async () => {
    // Without this the fix above is indistinguishable from a form that refuses
    // every payment, which would close the defect by removing the feature.
    const base = createMockClient({ seed: false });
    await seedBank(base);
    const { axisID, centreID } = await firstAxis(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F5" });
    await screen.findByTestId("single-entry-panel");
    fireEvent.mouseDown(await screen.findByText("HDFC Bank"));
    fireEvent.change(screen.getByTestId("single-ledger:0"), {
      target: { value: "Office Rent" },
    });
    fireEvent.change(screen.getByTestId("single-amount:0"), {
      target: { value: "2500" },
    });
    fireEvent.click(await screen.findByTestId("alloc:0-add"));
    fireEvent.change(screen.getByTestId("alloc:0-axis:0"), {
      target: { value: axisID },
    });
    fireEvent.change(screen.getByTestId("alloc:0-centre:0"), {
      target: { value: centreID },
    });
    // "rest" — the whole posting on this axis.
    fireEvent.click(screen.getByTestId("alloc:0-rest:0"));

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() => expect(spies.postPaymentVoucher).toHaveBeenCalledTimes(1));
    // The new expense account WAS created — the gate delays the write, it does
    // not remove it.
    expect(createdLedgerNames(spies)).toEqual(["Office Rent"]);

    const [, req] = spies.postPaymentVoucher!.mock.calls[0]!;
    const allocs = (req as { cost_allocations?: { amount: string; posting_seq: number }[] })
      .cost_allocations;
    expect(allocs).toHaveLength(1);
    expect(allocs![0]!.amount).toBe("2500.0000");
    // Posting 1, not 0 — posting 0 is the bank. Pinned here too because the
    // gate now sits between the two and must not have moved the offset.
    expect(allocs![0]!.posting_seq).toBe(1);
  });
});

describe("⛔ the master gate: the double-entry grid (D39, the SECOND grid)", () => {
  it("a cost axis short by any amount creates NO ledger and posts nothing", async () => {
    // ⛔ THE SAME DEFECT ON THE OTHER FORM, and this one is the sharper case: the
    // Journal builder ALREADY carried a comment saying an `ensure*` must never
    // run before validation, and it was still creating ledgers — because the
    // check that ran first was only the BALANCE check.
    const base = createMockClient({ seed: false });
    const { axisID, centreID } = await firstAxis(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F7" }); // Journal
    await screen.findByTestId("double-entry-panel");
    const rows: [string, "DR" | "CR", string][] = [
      ["Provision for Bonus", "CR", "1000"],
      ["Salaries", "DR", "1000"],
    ];
    for (let i = 0; i < rows.length; i++) {
      if (i > 0) fireEvent.click(screen.getByTestId("de-add-line"));
      fireEvent.change(screen.getByTestId(`de-ledger:${i}`), {
        target: { value: rows[i]![0] },
      });
      fireEvent.change(screen.getByTestId(`de-side:${i}`), {
        target: { value: rows[i]![1] },
      });
      fireEvent.change(screen.getByTestId(`de-amount:${i}`), {
        target: { value: rows[i]![2] },
      });
    }

    fireEvent.click(await screen.findByTestId("alloc:1-add"));
    fireEvent.change(screen.getByTestId("alloc:1-axis:0"), {
      target: { value: axisID },
    });
    fireEvent.change(screen.getByTestId("alloc:1-centre:0"), {
      target: { value: centreID },
    });
    fireEvent.change(screen.getByTestId("alloc:1-amount:0"), {
      target: { value: "600" },
    });

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toMatch(/short by 400/),
    );
    expect(screen.getByRole("status").textContent).toMatch(/Salaries/);
    // ⛔ THE VOUCHER BALANCES PERFECTLY — 1,000 against 1,000 — so the balance
    // refusal that used to stand here has nothing to say about it.
    expect(createdLedgerNames(spies)).toEqual([]);
    expect(spies.postJournalVoucher).not.toHaveBeenCalled();
  });

  it("⛔ CONTROL: the same Journal with the axis complete creates both ledgers and posts", async () => {
    const base = createMockClient({ seed: false });
    const { axisID, centreID } = await firstAxis(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F7" });
    await screen.findByTestId("double-entry-panel");
    const rows: [string, "DR" | "CR", string][] = [
      ["Provision for Bonus", "CR", "1000"],
      ["Salaries", "DR", "1000"],
    ];
    for (let i = 0; i < rows.length; i++) {
      if (i > 0) fireEvent.click(screen.getByTestId("de-add-line"));
      fireEvent.change(screen.getByTestId(`de-ledger:${i}`), {
        target: { value: rows[i]![0] },
      });
      fireEvent.change(screen.getByTestId(`de-side:${i}`), {
        target: { value: rows[i]![1] },
      });
      fireEvent.change(screen.getByTestId(`de-amount:${i}`), {
        target: { value: rows[i]![2] },
      });
    }
    fireEvent.click(await screen.findByTestId("alloc:1-add"));
    fireEvent.change(screen.getByTestId("alloc:1-axis:0"), {
      target: { value: axisID },
    });
    fireEvent.change(screen.getByTestId("alloc:1-centre:0"), {
      target: { value: centreID },
    });
    fireEvent.click(screen.getByTestId("alloc:1-rest:0"));

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.postJournalVoucher).toHaveBeenCalledTimes(1));
    expect(createdLedgerNames(spies).sort()).toEqual([
      "Provision for Bonus",
      "Salaries",
    ]);
  });
});

describe("⛔ the master gate: the ALTER path (D41, DRIVEN)", () => {
  /**
   * Store a Purchase that carries NO `voucher_gst_line` rows — which is exactly
   * what the browser found: `formFromView` hydrates the rate from
   * `gst_lines[0]`, so such a voucher opens with GST Rate % EMPTY, and the
   * accounting-mode builder then sends `gst_rate_pct: ""`, which `buildGST`
   * 400s on. The old order created "Input CGST/SGST/IGST" and "Purchases"
   * first.
   */
  async function storePlainPurchase(base: CoredClient): Promise<string> {
    const supplier = await base.createLedger("co-1", {
      name: "Zenith Supplies",
      group_name: "Sundry Creditors",
      state_code: "29",
    });
    const expense = await base.createLedger("co-1", {
      name: "Consumables",
      group_name: "Direct Expenses",
    });
    const res = await base.postPurchaseVoucher("co-1", {
      voucher_date: "2026-04-10",
      narration: "supplier bill, no GST lines stored",
      party: "Zenith Supplies",
      party_ledger_id: supplier.ledger_id,
      postings: [
        { ledger_id: expense.ledger_id, dr_cr: "DR", amount: "1000.0000" },
        { ledger_id: supplier.ledger_id, dr_cr: "CR", amount: "1000.0000" },
      ],
    });
    return res.voucher_no;
  }

  /** Day Book -> the row -> Enter, the way an operator drills. */
  async function drill(app: HTMLElement, voucherNo: string) {
    fireEvent.keyDown(app, { key: "Escape" });
    // ⛔ THE PHYSICAL KEY, WITH THE COMPOSED CHARACTER macOS ACTUALLY SENDS.
    // Option composes: a real Mac emits key:"©" for Alt+G and never "g", and
    // every Alt binding in this product was once found DEAD on macOS while the
    // tests pressed a key a Mac cannot send. `comboFromEvent` reads `code`.
    fireEvent.keyDown(app, { key: "©", code: "KeyG", altKey: true });
    fireEvent.mouseDown(screen.getByText("Day Book"));
    fireEvent.keyDown(app, { key: "Enter" });
    await waitFor(() => expect(screen.getByText(voucherNo)).toBeTruthy());
    fireEvent.keyDown(app, { key: "Enter" });
  }

  it("a refused ALTER creates no 'Purchases' ledger and no tax ledgers", async () => {
    const base = createMockClient({ seed: false });
    const voucherNo = await storePlainPurchase(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    await drill(app, voucherNo);
    await waitFor(() => expect(spies.getVoucher).toHaveBeenCalled());
    // The hydration really is the one the browser saw: no rate on the form.
    await waitFor(() =>
      expect((screen.getByTestId("gst-rate") as HTMLInputElement).value).toBe(""),
    );

    spies.createLedger!.mockClear();
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toMatch(/GST Rate/),
    );
    // It names what the SERVER would have said, and what to do about it.
    expect(screen.getByRole("status").textContent).toMatch(/invalid gst line gst_rate_pct/);
    // ⛔ THE ORACLE: the alter was refused and the chart is untouched. Before
    // the gate this ran ensureGstTaxLedgers (three accounts) and then
    // ensureLedger("Purchases") — four masters for a voucher that never wrote.
    expect(createdLedgerNames(spies)).toEqual([]);
    expect(spies.alterPurchaseVoucher).not.toHaveBeenCalled();
  });

  it("⛔ CONTROL: keying the rate makes the same alter go through", async () => {
    const base = createMockClient({ seed: false });
    const voucherNo = await storePlainPurchase(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    await drill(app, voucherNo);
    await waitFor(() => expect(spies.getVoucher).toHaveBeenCalled());
    await waitFor(() =>
      expect((screen.getByTestId("gst-rate") as HTMLInputElement).value).toBe(""),
    );

    // The refusal is escapable AT THE KEYBOARD — which is what makes it a
    // refusal and not a dead end (RULE 3).
    fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() => expect(spies.alterPurchaseVoucher).toHaveBeenCalledTimes(1));
    const [, , req] = spies.alterPurchaseVoucher!.mock.calls[0]!;
    expect((req as { gst?: GSTRequest }).gst?.lines?.[0]?.gst_rate_pct).toBe("18");
    // A PURCHASE, so the tax ledgers are the INPUT ones — the class's, never
    // "Output CGST" (which would book a reclaimable credit against a liability).
    expect(createdLedgerNames(spies).sort()).toEqual([
      "Input CGST",
      "Input IGST",
      "Input SGST",
      "Purchases",
    ]);
  });
});

describe("⛔ the master gate: the simplest keystroke in the product", () => {
  it("F8 then Ctrl+A on an UNTOUCHED form creates none of the three tax ledgers", async () => {
    // ⛔ THE CHEAPEST REPRODUCTION OF THE WHOLE CLASS, and it needed no cost
    // centres and no alter: an empty form ran `ensureGstTaxLedgers` (three
    // accounts) and then `ensureLedger({ name: "" })`, which cored refuses
    // outright — so the company gained Output CGST / SGST / IGST and no voucher,
    // and the operator was shown an error about a name they never typed.
    const base = createMockClient({ seed: false });
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F8" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toMatch(/Party A\/c Name is required/),
    );
    expect(createdLedgerNames(spies)).toEqual([]);
    expect(spies.postSalesVoucher).not.toHaveBeenCalled();
  });
});

describe("⛔ the master gate: the item invoice", () => {
  it("an empty grid creates none of the five masters an item invoice ensures", async () => {
    const base = createMockClient({ seed: false });
    await base.createLedger("co-1", {
      name: "Acme Traders",
      group_name: "Sundry Debtors",
      state_code: "29",
    });
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F8" });
    fireEvent.keyDown(app, { key: "h", ctrlKey: true });
    fireEvent.mouseDown(await screen.findByText("Item Invoice"));
    await screen.findByTestId("item-invoice-panel");
    fireEvent.mouseDown(await screen.findByText("Acme Traders"));

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toMatch(/at least one item line/),
    );
    // Three tax heads + Domestic Sales would have been minted for a body with
    // no goods on it.
    expect(createdLedgerNames(spies)).toEqual([]);
    expect(spies.postSalesVoucher).not.toHaveBeenCalled();
  });
});

/**
 * ===========================================================================
 * D60 — THE HSN THAT WAS NEVER REQUIRED, ON A DOCUMENT THAT CANNOT PRINT
 * WITHOUT IT.
 * ===========================================================================
 * A Sales invoice keyed through the GST panel SAVED happily and then could not
 * be printed: `printing.rule46` treats the HSN as a Rule 46(g) MANDATORY
 * PARTICULAR and answers 400 naming `items[0].hsn`. The operator discovers it at
 * the counter, with the customer waiting and the voucher already in the books.
 */
describe("D60 — a sales invoice may not be saved without its Rule 46(g) HSN", () => {
  async function seedCustomer(base: CoredClient) {
    await base.createLedger("co-1", {
      name: "Acme Traders",
      group_name: "Sundry Debtors",
      state_code: "29",
    });
  }

  it("refuses the save, names the rule, and creates no ledger on the way", async () => {
    const base = createMockClient({ seed: false });
    await seedCustomer(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F8" });
    fireEvent.mouseDown(await screen.findByText("Acme Traders"));
    fireEvent.change(screen.getByTestId("gst-taxable"), { target: { value: "10000" } });
    fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toMatch(/HSN \/ SAC is required/),
    );
    expect(screen.getByRole("status").textContent).toMatch(/Rule 46\(g\)/);
    expect(spies.postSalesVoucher).not.toHaveBeenCalled();
    // The refusal is inside the master gate, so it also closes the D39 window.
    expect(createdLedgerNames(spies)).toEqual([]);
  });

  it("⛔ CONTROL: an invoice that CARRIES an HSN still saves, and carries it on the wire", async () => {
    // ⛔ THE CONTROL THAT MAKES THE REFUSAL A FIX RATHER THAN A REGRESSION. A
    // change that refused every invoice would satisfy D60 and destroy invoicing.
    const base = createMockClient({ seed: false });
    await seedCustomer(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F8" });
    fireEvent.mouseDown(await screen.findByText("Acme Traders"));
    fireEvent.change(screen.getByTestId("gst-taxable"), { target: { value: "10000" } });
    fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });
    fireEvent.change(screen.getByTestId("gst-hsn"), { target: { value: "7326" } });

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const [, req] = spies.postSalesVoucher!.mock.calls[0]!;
    const gst = (req as { gst: GSTRequest }).gst;
    // ⛔ THE CODE REACHES THE WIRE. A refusal that let the field stay unsent
    // would only move the failure from the printer to GSTR-1 table 12.
    expect(gst.lines?.[0]?.hsn).toBe("7326");
    const postings = (req as { postings: PostingRequest[] }).postings;
    expect(postings.map((p) => p.amount)).toContain("11800.0000");
  });

  it("⛔ CONTROL: a PURCHASE is not refused — it is the supplier's document", async () => {
    // Scope is the fix. `docKindForClass` refuses to print a purchase at all
    // ("a purchase bill is the SUPPLIER'S document"), so requiring the HSN here
    // would refuse an entry for a document this product never issues — and would
    // make a scanned bill whose HSN did not survive OCR unacceptable.
    const base = createMockClient({ seed: false });
    await base.createLedger("co-1", {
      name: "Zenith Supplies",
      group_name: "Sundry Creditors",
      state_code: "29",
    });
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F9" });
    fireEvent.mouseDown(await screen.findByText("Zenith Supplies"));
    fireEvent.change(screen.getByTestId("gst-taxable"), { target: { value: "5000" } });
    fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.postPurchaseVoucher).toHaveBeenCalledTimes(1));
  });

  it("the ITEM grid DISCLOSES a stock master with no HSN instead of refusing it", async () => {
    // ⛔ RULE 3: NEVER SHIP A REFUSAL WHOSE REMEDY CANNOT BE REACHED. An item
    // line's HSN comes off the STOCK MASTER and this build has no keyboard path
    // to one (§7.1 D36 — Alt+C reaches ledgers only), so refusing here would
    // make item invoicing impossible on any company with one incomplete master.
    // It is said out loud instead, with the item named, before Ctrl+P.
    const base = createMockClient({ seed: false });
    await base.createLedger("co-1", {
      name: "Acme Traders",
      group_name: "Sundry Debtors",
      state_code: "29",
    });
    await base.createStockItem("co-1", {
      name: "Unclassified Widget",
      unit_symbol: "Nos",
      hsn: "",
      gst_rate_pct: "18.0000",
    });
    const withHsn = await base.createStockItem("co-1", {
      name: "Widget",
      unit_symbol: "Nos",
      hsn: "8481",
      gst_rate_pct: "18.0000",
    });
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F8" });
    fireEvent.keyDown(app, { key: "h", ctrlKey: true });
    fireEvent.mouseDown(await screen.findByText("Item Invoice"));
    await screen.findByTestId("item-invoice-panel");
    fireEvent.mouseDown(await screen.findByText("Acme Traders"));

    // ⛔ ABSENT FIRST, so its appearance below is not the note having been there
    // all along.
    expect(screen.queryByTestId("item-hsn-missing")).toBeNull();

    const items = (await base.listStockItems("co-1")).stock_items;
    const blank = items.find((i) => i.name === "Unclassified Widget")!;
    fireEvent.change(screen.getByTestId("item-select:0"), {
      target: { value: blank.id },
    });
    fireEvent.change(screen.getByTestId("item-qty:0"), { target: { value: "2" } });
    fireEvent.change(screen.getByTestId("item-rate:0"), { target: { value: "500" } });

    const note = await screen.findByTestId("item-hsn-missing");
    expect(note.textContent).toMatch(/Unclassified Widget/);
    expect(note.textContent).toMatch(/Rule 46\(g\)/);

    // ...and it SAVES. Disclosure, not refusal.
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));

    // ⛔ CONTROL: the note is about THIS master, not about item mode. Switching
    // the line to an item that carries a code clears it.
    fireEvent.change(screen.getByTestId("item-select:0"), {
      target: { value: withHsn.stock_item_id },
    });
    await waitFor(() => expect(screen.queryByTestId("item-hsn-missing")).toBeNull());
  });
});
