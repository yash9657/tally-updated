import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { CompanyProvider } from "../app/CompanyContext";
import { AppShell } from "../app/AppShell";
import { createMockClient } from "../api/mock";
import type { CoredClient } from "../api";

/**
 * Ctrl+P DRIVEN THROUGH THE REAL SHELL — invariants I-2, I-5 and I-6 of
 * docs/design/t1t4-ui-model.md.
 *
 * usePrint.test.tsx pins the handler in isolation. This suite exists because the
 * two failures that actually cost a user are properties of the WHOLE path:
 *
 *  - I-2: Ctrl+P must not reach the browser. Before the key was bound, AppShell's
 *    `if (!action) return` let it through and the browser printed a screenshot of
 *    the DOM — no Rule 46 particulars, no GSTIN. That is not a missing feature,
 *    it is a wrong one, and only a test at this level can see it.
 *  - I-6: the just-saved voucher slot must be CLEARED when the form's identity is
 *    dropped, or Ctrl+P prints the previous voucher — worse than refusing.
 */

function spyClient() {
  const base = createMockClient({ seed: true });
  // Typed with the real signature so the assertions below can read the class and
  // id the screen actually asked for.
  const printVoucher = vi.fn(
    async (_companyID: string, _voucherClass: string, _voucherID: string) => ({
      blob: new Blob(["%PDF-1.4"], { type: "application/pdf" }),
      filename: "Invoice-INV-0001.pdf",
    }),
  );
  return {
    client: { ...base, printVoucher } as unknown as CoredClient,
    printVoucher,
  };
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

const app = () => screen.getByRole("application");
const pressCtrlP = () => fireEvent.keyDown(app(), { key: "p", ctrlKey: true });

beforeEach(() => {
  // openPdfInBrowser is the default sink and it calls window.open + createObjectURL,
  // neither of which jsdom implements usefully. Stub them so the path runs.
  vi.stubGlobal("open", vi.fn(() => ({}) as Window));
  if (!URL.createObjectURL) {
    Object.defineProperty(URL, "createObjectURL", { value: vi.fn(() => "blob:x"), writable: true });
    Object.defineProperty(URL, "revokeObjectURL", { value: vi.fn(), writable: true });
  } else {
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:x");
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
  }
});

describe("Ctrl+P never reaches the browser (I-2)", () => {
  it("is consumed by the app, with preventDefault called", () => {
    renderShell(spyClient().client);
    fireEvent.keyDown(app(), { key: "F8" }); // a Sales voucher

    // fireEvent returns false when preventDefault was called on the event.
    const notPrevented = fireEvent.keyDown(app(), { key: "p", ctrlKey: true });
    expect(
      notPrevented,
      "Ctrl+P was left to the browser — it would open the print dialog and print a screenshot of the screen instead of the server's Rule 46 document",
    ).toBe(false);
  });

  it("says something — the key is never silently dead", async () => {
    renderShell(spyClient().client);
    fireEvent.keyDown(app(), { key: "F8" });
    pressCtrlP();
    // An unsaved voucher: a refusal, but a SPOKEN one.
    expect(await screen.findByText(/has not been saved/i)).toBeTruthy();
  });
});

describe("printing the invoice you just keyed (§2.1)", () => {
  it("prints after Ctrl+A, without needing to reopen it from the Day Book", async () => {
    const { client, printVoucher } = spyClient();
    renderShell(client);

    fireEvent.keyDown(app(), { key: "F8" }); // Sales
    // Fill just enough for the mock to accept: the party and a taxable value.
    const party = await screen.findByLabelText(/party/i);
    fireEvent.change(party, { target: { value: "Acme Traders" } });
    // ⛔ ENTER, BECAUSE TYPING ALONE SELECTS NOTHING. `SelectionList` is a
    // type-ahead: the query is local until Enter (or a click) fires onSelect, so
    // a test that only typed left the form's party EMPTY — and the builder then
    // called ensureLedger({ name: "" }), which real cored refuses outright
    // ("masters: ledger name is required"). The screen now refuses first, so the
    // test presses the key an operator presses.
    fireEvent.keyDown(party, { key: "Enter" });
    const taxable = screen.getByLabelText(/taxable value/i);
    fireEvent.change(taxable, { target: { value: "1000" } });
    // ⛔ THE RATE AND THE HSN ARE PART OF "just enough", AND WERE MISSING. The
    // rate is what `buildGST` 400s on when it cannot read it, and the HSN is a
    // Rule 46(g) particular WITHOUT WHICH THE SERVER REFUSES TO PRINT (D60) —
    // so a test that saved without them was pressing Ctrl+P on a document real
    // cored would not have rendered. The mock is lenient; the printer is not.
    fireEvent.change(screen.getByLabelText(/GST Rate/i), { target: { value: "18" } });
    fireEvent.change(screen.getByLabelText(/HSN/i), { target: { value: "7326" } });

    fireEvent.keyDown(app(), { key: "a", ctrlKey: true }); // save
    await waitFor(() => expect(screen.getByText(/^Saved$/)).toBeTruthy());

    pressCtrlP();
    await waitFor(() => expect(printVoucher).toHaveBeenCalled());
    // The class is the kebab-case path segment, and the id is the SERVER's.
    expect(printVoucher.mock.calls[0]![1]).toBe("sales");
    expect(printVoucher.mock.calls[0]![2]).toBeTruthy();
  });
});

describe("the stale-print guard (I-6)", () => {
  it("refuses after Alt+X rather than printing the PREVIOUS voucher", async () => {
    const { client, printVoucher } = spyClient();
    renderShell(client);

    fireEvent.keyDown(app(), { key: "F8" });
    const party = await screen.findByLabelText(/party/i);
    fireEvent.change(party, { target: { value: "Acme Traders" } });
    // ⛔ ENTER, BECAUSE TYPING ALONE SELECTS NOTHING. `SelectionList` is a
    // type-ahead: the query is local until Enter (or a click) fires onSelect, so
    // a test that only typed left the form's party EMPTY — and the builder then
    // called ensureLedger({ name: "" }), which real cored refuses outright
    // ("masters: ledger name is required"). The screen now refuses first, so the
    // test presses the key an operator presses.
    fireEvent.keyDown(party, { key: "Enter" });
    fireEvent.change(screen.getByLabelText(/taxable value/i), {
      target: { value: "1000" },
    });
    // See the note above: a sales invoice does not save without these.
    fireEvent.change(screen.getByLabelText(/GST Rate/i), { target: { value: "18" } });
    fireEvent.change(screen.getByLabelText(/HSN/i), { target: { value: "7326" } });
    fireEvent.keyDown(app(), { key: "a", ctrlKey: true });
    await waitFor(() => expect(screen.getByText(/^Saved$/)).toBeTruthy());

    // Alt+X — abandon the entry. The form's identity is dropped.
    fireEvent.keyDown(app(), { key: "x", altKey: true });
    await waitFor(() =>
      expect(screen.getByText(/Voucher cancelled/i)).toBeTruthy(),
    );

    printVoucher.mockClear();
    pressCtrlP();

    // It must REFUSE, and above all must not fetch the voucher keyed a moment ago.
    expect(await screen.findByText(/has not been saved/i)).toBeTruthy();
    expect(
      printVoucher,
      "Ctrl+P printed the voucher that was cancelled — a stale document is worse than a refusal",
    ).not.toHaveBeenCalled();
  });
});
