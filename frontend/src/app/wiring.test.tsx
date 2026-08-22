import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
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
import {
  _resetLedgerCache,
  _resetOutputTaxLedgerCache,
} from "../screens/vouchers/gstVoucher";

afterEach(cleanup);
/**
 * ⛔ THE LEDGER MEMOS ARE MODULE-LEVEL AND KEYED ON THE COMPANY ID, and every
 * test in this file is company "co-1" on a FRESH mock. Without this reset the
 * second test resolves ids minted inside the first test's client — ids that do
 * not exist in its own books — and the failure reads as a wiring bug rather
 * than as leaked state. (The same reset guards
 * VoucherScreen.masterGate.test.tsx, which is where the hazard was first hit.)
 */
beforeEach(() => {
  _resetLedgerCache();
  _resetOutputTaxLedgerCache();
});

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
      // ⛔ THE HOME STATE IS STATED. Without it the supplier's state code is ""
      // and every invoice these tests key comes out INTER-state (IGST) against a
      // party whose own code is 29 — so an intra-state sale would be re-derived
      // as inter-state the moment it was altered. The wiring assertions below do
      // not read the split, but a fixture that quietly changes the tax kind is
      // not a fixture anyone should copy.
      initialCompany={{ id: "co-1", name: "Test Co", homeStateCode: "29" }}
    >
      <AppShell />
    </CompanyProvider>,
  );
}

/** Every master this accept had to CREATE, by name (see `createdLedgerNames`). */
function createdLedgerNames(
  spies: Record<string, ReturnType<typeof vi.fn>>,
): string[] {
  return spies.createLedger!.mock.calls.map(
    (c) => (c[1] as { name: string }).name,
  );
}

/**
 * The two counterparties an invoice is raised ON, in the chart before the form
 * opens — so the sales and purchase cases PICK a real party the way an operator
 * does, and so an accept that re-mints one is visible as a `createLedger` call.
 */
async function seedCounterparties(
  base: CoredClient,
): Promise<{ debtor: string; creditor: string }> {
  const debtor = await base.createLedger("co-1", {
    name: "Acme Traders",
    group_name: "Sundry Debtors",
    state_code: "29",
  });
  const creditor = await base.createLedger("co-1", {
    name: "Zenith Supplies",
    group_name: "Sundry Creditors",
    state_code: "29",
  });
  return { debtor: debtor.ledger_id, creditor: creditor.ledger_id };
}

/**
 * A REAL sales invoice to drill into and alter: party, base and the three tax
 * ledgers all present, and a stored `voucher_gst_line`.
 *
 * ⛔⛔ THE `gst` BLOCK IS THE FIXTURE'S WHOLE POINT, AND ITS ABSENCE WAS A
 * DEFECT THIS SUITE PINNED — see the alter test's own note. `formFromView`
 * hydrates GST Rate % from `gst_lines[0]`, so a voucher stored without them
 * opens with the rate EMPTY, and the accounting-mode builder then sends
 * `gst_rate_pct: ""`, which cored's `buildGST` answers 400 to (server.go:2552,
 * "invalid gst line gst_rate_pct" — §7.1 D41's exact wire error). The mock does
 * not parse decimals, so the old fixture round-tripped here and 400'd in a
 * browser: the suite was certifying a body the server refuses.
 */
async function seedSalesVoucher(client: CoredClient) {
  const debtor = await client.createLedger("co-1", {
    name: "Acme Corp",
    group_name: "Sundry Debtors",
    state_code: "29",
  });
  const sales = await client.createLedger("co-1", {
    name: "Domestic Sales",
    group_name: "Sales Accounts",
  });
  const [cgst, sgst, igst] = await Promise.all([
    client.createLedger("co-1", { name: "Output CGST", group_name: "Duties & Taxes" }),
    client.createLedger("co-1", { name: "Output SGST", group_name: "Duties & Taxes" }),
    client.createLedger("co-1", { name: "Output IGST", group_name: "Duties & Taxes" }),
  ]);
  const posted = await client.postSalesVoucher("co-1", {
    voucher_no: "INV/0001",
    voucher_date: "2026-04-10",
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
      lines: [{ taxable_value: "10000.0000", gst_rate_pct: "18", hsn: "7326" }],
      cgst_ledger_id: cgst.ledger_id,
      sgst_ledger_id: sgst.ledger_id,
      igst_ledger_id: igst.ledger_id,
    },
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

  // ⛔⛔ AND SO DO THE TWO GST CLASSES — WHICH IS THE SAME LESSON, A THIRD TIME.
  //
  // THESE TWO CASES USED TO PASS ON AN UNTOUCHED FORM, AND THAT WAS THE BUG THEY
  // CERTIFIED. Press F8 and Ctrl+A on a form with nothing keyed at all and the
  // builder ran `ensureGstTaxLedgers` (minting Output CGST / SGST / IGST) and
  // then `ensureLedger({ name: "" })`, which cored refuses outright ("masters:
  // ledger name is required") — three real accounts, no voucher, and the
  // operator shown one error about a name they never typed. That is §7.1 D39's
  // cheapest reproduction, and this suite asserted the POST as correct
  // behaviour: on the mock, `postSalesVoucher` really was reached, because the
  // mock does not refuse an empty ledger name the way the server does.
  //
  // The form now REFUSES an invoice with no party before it creates anything
  // (`partyRefusal`), so these cases state a real invoice the way an operator
  // keys one. The refusal itself, and its master-count oracle, are covered in
  // VoucherScreen.masterGate.test.tsx.
  //
  // ⚠️ THE HSN IS ON SALES ONLY, and that scope is D60's fix rather than an
  // omission here: a tax invoice may not be SAVED without its Rule 46(g)
  // particular, because `printing.rule46` will not render one without it; a
  // purchase is the supplier's document and this product never prints one.
  const GST_ENTRY: Record<string, { party: string; hsn?: string }> = {
    sales: { party: "Acme Traders", hsn: "7326" },
    purchase: { party: "Zenith Supplies" },
  };

  for (const { fkey, cls, method } of cases) {
    it(`Ctrl+A in a ${cls} voucher calls ${method} with empty voucher_no and shows the returned number`, async () => {
      const base = createMockClient({ seed: false });
      const seeded = await seedCounterparties(base);
      const { client, spies } = spyClient(base);
      renderShell(client);
      const app = screen.getByRole("application");
      fireEvent.keyDown(app, { key: fkey });

      const gstEntry = GST_ENTRY[cls];
      if (gstEntry) {
        fireEvent.mouseDown(await screen.findByText(gstEntry.party));
        fireEvent.change(screen.getByTestId("gst-taxable"), {
          target: { value: "10000" },
        });
        fireEvent.change(screen.getByTestId("gst-rate"), {
          target: { value: "18" },
        });
        if (gstEntry.hsn) {
          fireEvent.change(screen.getByTestId("gst-hsn"), {
            target: { value: gstEntry.hsn },
          });
        }
      }

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

      // ⛔ AND THE DOCUMENT NAMES SOMEBODY. `party` / `party_ledger_id` are what
      // printingdb builds the Party block from, so a body that reaches the
      // server unnamed is a document that cannot be handed to anyone.
      const named = req as { party?: string; party_ledger_id?: string };
      if (gstEntry) {
        expect(named.party).toBe(gstEntry.party);
        expect(named.party_ledger_id).toBe(
          cls === "purchase" ? seeded.creditor : seeded.debtor,
        );
        // ⛔ RESOLVED, NOT RE-MINTED. The party was picked off the chart, so an
        // accept that CREATES a ledger by that name has just made a second
        // "Acme Traders" for the same customer — which is what carrying the real
        // id exists to prevent.
        expect(createdLedgerNames(spies)).not.toContain(gstEntry.party);
      }
      if (SINGLE_ENTRY[cls]) {
        // ⛔ D59: THE BANK IS NOT THE COUNTERPARTY. The account the money moved
        // through is `postings[0]`; the party is a PARTICULAR. Naming HDFC Bank
        // here is what printed "Received From: HDFC Bank" on every receipt.
        expect(named.party).toBe("Office Rent");
        expect(named.party).not.toBe("HDFC Bank");
      }
    });
  }
});

describe("VoucherScreen Ctrl+A alter wiring (drill-loaded)", () => {
  /**
   * ⛔⛔ THE FIXTURE CHANGED AND THAT IS THE FIX — §7.1 D41, DRIVEN IN A BROWSER.
   *
   * This test used to drill into a sales voucher stored with NO `voucher_gst_line`
   * rows, and assert that Ctrl+A called `alterSalesVoucher`. It did, against the
   * mock. Against real cored the identical keystroke created a ledger over the
   * wire and THEN failed the write with "invalid gst line gst_rate_pct" — a
   * refused ALTER that left a master behind — because the rate hydrates EMPTY
   * from a voucher that has no GST lines and `buildGST` cannot parse "".
   *
   * So the case was certifying a body the server refuses, on the strength of a
   * mock that is looser than the handler. The ARC is unchanged and is the thing
   * worth pinning: a drill-loaded frame PUTs to the per-class alter route with
   * the loaded id, and resends the number and date it loaded. What changed is
   * that the voucher on screen is now one the operator could actually save — a
   * real tax invoice — and the assertions are stronger for it.
   *
   * ⚠️ The REFUSAL side (a plain voucher's alter is refused, and creates no
   * master on the way) is pinned in VoucherScreen.masterGate.test.tsx, with the
   * control that keying the rate lets the same alter through.
   */
  it("resends the loaded voucher_no/date and calls alterSalesVoucher with the loaded id", async () => {
    const base = createMockClient({ seed: false });
    const { posted, debtor } = await seedSalesVoucher(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    // Drill: Day Book -> highlight the row -> Enter into the alter voucher frame.
    // ⛔ THE PHYSICAL KEY. Option COMPOSES on macOS: a real Mac sends key:"©" for
    // Alt+G and never "g", and `comboFromEvent` reads `code` for exactly that
    // reason. Every Alt+letter binding in this product was once found DEAD on a
    // Mac while tests like this one pressed a key a Mac cannot send.
    fireEvent.keyDown(app, { key: "©", code: "KeyG", altKey: true });
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
    // The stored rate came back onto the form — which is what makes this a
    // voucher the accounting-mode builder can faithfully restate.
    expect((screen.getByTestId("gst-rate") as HTMLInputElement).value).toBe(
      "18.0000",
    );

    spies.createLedger!.mockClear();
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.alterSalesVoucher).toHaveBeenCalledTimes(1));
    const [companyID, voucherID, req] =
      spies.alterSalesVoucher!.mock.calls[0]!;
    expect(companyID).toBe("co-1");
    expect(voucherID).toBe(posted.voucher_id);
    expect((req as { voucher_no?: string }).voucher_no).toBe("INV/0001");
    expect((req as { voucher_date?: string }).voucher_date).toBe("2026-04-10");

    // ⛔ A PUT, NOT A POST. An alter that fell through to the create route would
    // leave the original standing and mint a SECOND invoice for the same sale —
    // and both would be in the Day Book, balancing.
    expect(spies.postSalesVoucher).not.toHaveBeenCalled();
    // ⛔ AND IT KEPT THE CUSTOMER IT LOADED. Re-resolving the party by NAME is
    // how an alter comes to point at a different (or a brand-new) ledger.
    expect((req as { party_ledger_id?: string }).party_ledger_id).toBe(
      debtor.ledger_id,
    );
    // ⛔ THE D41 ORACLE, on the path that is meant to SUCCEED: every master this
    // voucher needs — the party, Domestic Sales, the three tax heads — is
    // already in the chart, so an alter that CREATES anything has minted a
    // duplicate. Zero is the only right answer, and the count is reported by
    // NAME so a failure says which account was minted.
    expect(createdLedgerNames(spies)).toEqual([]);
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
    // ⛔ D87: a party now needs a STATE CODE or the form refuses client-side
    // and sends nothing. Stated here rather than the refusal being weakened:
    // a party with no place of supply is the input that made D19 derive an
    // intra-state bill as IGST.
    fireEvent.change(screen.getByTestId("master-state-code"), {
      target: { value: "29" },
    });
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
