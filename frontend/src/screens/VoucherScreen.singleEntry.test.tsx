import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";
import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient } from "../api";
import type { CoredClient, PostingRequest } from "../api";
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
 * SINGLE-ENTRY MODE (Ctrl+H) for Payment / Receipt / Contra.
 *
 * Tally defaults those three to single entry — "one account on one side,
 * multiple on the other" (RESEARCH.md §"Voucher entry") — which is why bulk cash
 * work is fast there. `voucherConfig` has declared `entry: "single"` since Phase
 * 1 and NOTHING read it: every voucher rendered the same form, and Ctrl+H opened
 * a picker whose three options did nothing at all.
 *
 * THE CLAIM THAT MATTERS IS ORIENTATION, not layout. Single entry is a
 * data-capture affordance; what gets posted is an ordinary balanced voucher, and
 * the server's double-entry constraint catches an UNBALANCED body but NOT a
 * REVERSED one. This project has already shipped that exact failure once (WS8b:
 * a purchase built by the sales-shaped builder posted reversed and still passed
 * the server's dr_cr-blind GST verify). So the side of every posting is pinned
 * here, by class.
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
      initialCompany={{ id: "co-1", name: "Test Co", homeStateCode: "29" }}
    >
      <AppShell />
    </CompanyProvider>,
  );
}

/**
 * Resolve the posted ledger_ids back to names.
 *
 * Counting sides is NOT enough to pin orientation. A one-particular voucher has
 * exactly one debit and one credit whichever way round it is built, so a test
 * that asserts only lengths and amounts passes just as happily on a REVERSED
 * voucher. Every orientation assertion below therefore names the ledger it
 * expects on each side.
 */
async function sidesByName(
  client: CoredClient,
  postings: PostingRequest[],
): Promise<{ debits: [string, string][]; credits: [string, string][] }> {
  const { ledgers } = await client.listLedgers("co-1");
  const nameOf = new Map(ledgers.map((l) => [l.id, l.name]));
  const on = (side: string): [string, string][] =>
    postings
      .filter((p) => p.dr_cr.toUpperCase().startsWith(side))
      .map((p): [string, string] => [nameOf.get(p.ledger_id) ?? p.ledger_id, p.amount])
      .sort((a, b) => a[0].localeCompare(b[0]));
  return { debits: on("D"), credits: on("C") };
}

/** Fill the single-entry form: one account, then N particulars. */
async function fillSingleEntry(
  account: string,
  lines: [string, string][],
): Promise<void> {
  fireEvent.change(screen.getByRole("combobox", { name: "Account" }), {
    target: { value: account },
  });
  fireEvent.mouseDown(await screen.findByText(new RegExp(account)));
  for (let i = 0; i < lines.length; i++) {
    if (i > 0) fireEvent.click(screen.getByTestId("single-add-line"));
    fireEvent.change(screen.getByTestId(`single-ledger:${i}`), {
      target: { value: lines[i]![0] },
    });
    fireEvent.change(screen.getByTestId(`single-amount:${i}`), {
      target: { value: lines[i]![1] },
    });
  }
}

describe("single-entry mode is the DEFAULT for Payment / Receipt / Contra", () => {
  for (const [fkey, cls] of [
    ["F5", "payment"],
    ["F6", "receipt"],
    ["F4", "contra"],
  ] as const) {
    it(`${fkey} (${cls}) opens in single entry without touching Ctrl+H`, async () => {
      renderShell(spyClient().client);
      fireEvent.keyDown(screen.getByRole("application"), { key: fkey });
      expect(await screen.findByTestId("single-entry-panel")).toBeTruthy();
    });
  }

  it("F7 (journal) does NOT — both sides are the point there, as in Tally", async () => {
    renderShell(spyClient().client);
    fireEvent.keyDown(screen.getByRole("application"), { key: "F7" });
    expect(screen.queryByTestId("single-entry-panel")).toBeNull();
  });

  it("F8 (sales) does NOT — it is a GST invoice form", async () => {
    renderShell(spyClient().client);
    fireEvent.keyDown(screen.getByRole("application"), { key: "F8" });
    expect(screen.queryByTestId("single-entry-panel")).toBeNull();
  });
});

describe("single entry posts a BALANCED, correctly-ORIENTED voucher", () => {
  it("a PAYMENT credits the bank once and debits each expense", async () => {
    const { client, spies } = spyClient();
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F5" });

    await fillSingleEntry("HDFC Bank", [
      ["Office Rent", "2500"],
      ["Electricity", "1500"],
    ]);
    // The single side's amount is DERIVED and shown before saving.
    expect(screen.getByTestId("single-total").textContent).toContain("4000.0000");

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.postPaymentVoucher).toHaveBeenCalledTimes(1));

    const [, req] = spies.postPaymentVoucher!.mock.calls[0]!;
    const postings = (req as { postings: PostingRequest[] }).postings;
    expect(postings).toHaveLength(3);

    // ORIENTATION: money OUT credits the bank; the expenses are debited. Named
    // by LEDGER, so a builder that put the right amounts on the wrong accounts
    // cannot pass.
    const { debits, credits } = await sidesByName(client, postings);
    expect(credits).toEqual([["HDFC Bank", "4000.0000"]]);
    expect(debits).toEqual([
      ["Electricity", "1500.0000"],
      ["Office Rent", "2500.0000"],
    ]);

    // BALANCED to the paisa — the sum is computed in paise, so two lines cannot
    // drift by a rounding cent.
    const sum = (ps: [string, string][]) =>
      ps.reduce((t, [, a]) => t + Math.round(parseFloat(a) * 10000), 0);
    expect(sum(debits)).toBe(sum(credits));
  });

  it("a RECEIPT is the MIRROR image: the bank is debited, income credited", async () => {
    // The control that makes the payment test meaningful. Without it, a builder
    // that credited the account unconditionally would pass above and be wrong
    // for every receipt in the product.
    //
    // TWO particulars, deliberately. With one, the voucher has exactly one debit
    // and one credit whichever way round it is built, and a count-and-amount
    // assertion is invariant under reversal — which is how this test formerly
    // stayed green while the bank was CREDITED on money coming IN.
    const { client, spies } = spyClient();
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F6" });

    await fillSingleEntry("HDFC Bank", [
      ["Consulting Income", "9000"],
      ["Interest Received", "1000"],
    ]);
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.postReceiptVoucher).toHaveBeenCalledTimes(1));

    const [, req] = spies.postReceiptVoucher!.mock.calls[0]!;
    const postings = (req as { postings: PostingRequest[] }).postings;
    const { debits, credits } = await sidesByName(client, postings);
    expect(debits).toEqual([["HDFC Bank", "10000.0000"]]);
    expect(credits).toEqual([
      ["Consulting Income", "9000.0000"],
      ["Interest Received", "1000.0000"],
    ]);
  });

  it("a CONTRA credits the source account and debits the destination", async () => {
    // Contra had single-entry mode and NO posting test at all: reversing its
    // orientation alone left the whole suite green. Cash withdrawn from the bank
    // CREDITS the bank and DEBITS cash.
    const { client, spies } = spyClient();
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F4" });

    await fillSingleEntry("HDFC Bank", [
      ["Cash", "5000"],
      ["Petty Cash", "500"],
    ]);
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.postContraVoucher).toHaveBeenCalledTimes(1));

    const [, req] = spies.postContraVoucher!.mock.calls[0]!;
    const postings = (req as { postings: PostingRequest[] }).postings;
    const { debits, credits } = await sidesByName(client, postings);
    expect(credits).toEqual([["HDFC Bank", "5500.0000"]]);
    expect(debits).toEqual([
      ["Cash", "5000.0000"],
      ["Petty Cash", "500.0000"],
    ]);
  });

  it("refuses to post with no particulars rather than saving an empty voucher", async () => {
    // The pre-existing behaviour POSTED an empty voucher, which the server
    // accepted because zero debits equal zero credits. A voucher with no lines
    // is not a voucher.
    const { client, spies } = spyClient();
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F5" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toMatch(/particular/i),
    );
    expect(spies.postPaymentVoucher).not.toHaveBeenCalled();
  });
});

describe("Ctrl+H Change Mode", () => {
  it("offers the modes for THIS class and marks the one in force", async () => {
    renderShell(spyClient().client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F5" });
    fireEvent.keyDown(app, { key: "h", ctrlKey: true });

    expect(await screen.findByTestId("change-mode-overlay")).toBeTruthy();
    expect(screen.getByText(/Single Entry\s+← current/)).toBeTruthy();
    expect(screen.getByText("Double Entry")).toBeTruthy();
    // NOT the invoice modes: they are Sales/Purchase's, and offering them here
    // is what the old overlay did on every voucher.
    expect(screen.queryByText(/Item Invoice/)).toBeNull();
  });

  it("switching to double entry replaces the single-entry form", async () => {
    renderShell(spyClient().client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F5" });
    expect(await screen.findByTestId("single-entry-panel")).toBeTruthy();

    fireEvent.keyDown(app, { key: "h", ctrlKey: true });
    fireEvent.mouseDown(await screen.findByText("Double Entry"));

    await waitFor(() => expect(screen.queryByTestId("single-entry-panel")).toBeNull());
    // ...and the overlay closed itself, Tally-style — no confirming Enter.
    expect(screen.queryByTestId("change-mode-overlay")).toBeNull();
  });

  it("on a SALES voucher it offers the INVOICE modes, not single/double entry", async () => {
    // Sales/Purchase get Tally's invoice family. Item Invoice and Accounting
    // Invoice are both real now (INV-I); "As Voucher" is genuinely not built
    // and still says so — the same honesty the keymap applies to unavailable
    // combos, because a key that silently does nothing is what makes a keyboard
    // user distrust the app. Picking the not-built one dismisses and changes
    // nothing, and single entry is never offered on a class that has no single
    // side to infer.
    renderShell(spyClient().client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" });
    fireEvent.keyDown(app, { key: "h", ctrlKey: true });

    expect(await screen.findByTestId("change-mode-overlay")).toBeTruthy();
    expect(screen.getByText(/^Item Invoice$/)).toBeTruthy();
    expect(screen.queryByText(/^Single Entry/)).toBeNull();

    fireEvent.mouseDown(screen.getByText(/As Voucher — not built/));
    await waitFor(() => expect(screen.queryByTestId("change-mode-overlay")).toBeNull());
    expect(screen.queryByTestId("single-entry-panel")).toBeNull();
    // Dismissing a not-built mode leaves the accounting invoice in force.
    expect(screen.queryByTestId("item-invoice-panel")).toBeNull();
    expect(screen.getByTestId("gst-panel")).toBeTruthy();
  });
});

/**
 * ===========================================================================
 * ⛔⛔ WHO THE DOCUMENT IS FOR — §7.1 D59, FOUND ON THE PRINTED PAPER.
 * ===========================================================================
 *
 * The printed Receipt and Payment named the BANK as the counterparty —
 * "Received From: HDFC Bank", "Paid To: HDFC Bank" — because this form put the
 * bank in BOTH `party` and `party_ledger_id`, and `printingdb` builds the Party
 * block from `party_ledger_id` (falling back to the `party` LABEL when there is
 * no master behind it). A receipt that says the money came from your own bank
 * credits nobody's account; `vdPartyConsequence` states exactly that.
 *
 * ⛔ THE ASSERTIONS NAME LEDGERS ON BOTH SIDES, not just the party field. The
 * fix moves what `party_ledger_id` MEANS on this form, and the way to get that
 * catastrophically wrong is to also move the ACCOUNT leg — a receipt rebuilt as
 * `Dr Acme / Cr Acme` balances to the paisa and loses the bank entirely, and no
 * total, trigger or server check disagrees with it.
 */
describe("D59 — the counterparty is a PARTICULAR, never the bank", () => {
  async function seedBooks(base: CoredClient): Promise<void> {
    await base.createLedger("co-1", {
      name: "HDFC Bank",
      group_name: "Bank Accounts",
    });
    await base.createLedger("co-1", {
      name: "Acme Traders",
      group_name: "Sundry Debtors",
      state_code: "29",
    });
    await base.createLedger("co-1", {
      name: "Bharat Stores",
      group_name: "Sundry Debtors",
      state_code: "29",
    });
  }

  it("a RECEIPT names the CUSTOMER, and still debits the bank", async () => {
    const base = createMockClient({ seed: false });
    await seedBooks(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F6" });

    await fillSingleEntry("HDFC Bank", [["Acme Traders", "5000"]]);
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.postReceiptVoucher).toHaveBeenCalledTimes(1));

    const [, req] = spies.postReceiptVoucher!.mock.calls[0]!;
    const acme = (await client.listLedgers("co-1")).ledgers.find(
      (l) => l.name === "Acme Traders",
    )!;
    // ⛔ THE PRINTED "Received From". Both the label and the master.
    expect((req as { party: string }).party).toBe("Acme Traders");
    expect((req as { party_ledger_id?: string }).party_ledger_id).toBe(acme.id);

    // ⛔ AND THE MONEY STILL MOVED THE SAME WAY. This is the control that makes
    // the change a re-labelling rather than a re-posting: the bank is debited
    // the total and the customer credited, exactly as before.
    const postings = (req as { postings: PostingRequest[] }).postings;
    const { debits, credits } = await sidesByName(client, postings);
    expect(debits).toEqual([["HDFC Bank", "5000.0000"]]);
    expect(credits).toEqual([["Acme Traders", "5000.0000"]]);
  });

  it("a PAYMENT to an expense head names it as a LABEL and sends no party master", async () => {
    // ⛔ `party_ledger_id` IS A PARTY MASTER, and "Electricity" is an expense
    // head. "Paid To: Electricity" is the truth about the document and prints
    // from the label; pointing the master field at an expense ledger would put
    // a non-party into a column that gstr1/itc/ewaybill all join on.
    const base = createMockClient({ seed: false });
    await seedBooks(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F5" });

    await fillSingleEntry("HDFC Bank", [["Electricity", "1500"]]);
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.postPaymentVoucher).toHaveBeenCalledTimes(1));

    const [, req] = spies.postPaymentVoucher!.mock.calls[0]!;
    expect((req as { party: string }).party).toBe("Electricity");
    expect((req as { party_ledger_id?: string }).party_ledger_id).toBeUndefined();
    // ⛔ AND NOT THE BANK, which is what it used to be on both fields.
    expect((req as { party: string }).party).not.toBe("HDFC Bank");
  });

  it("two customers on one receipt name NOBODY rather than guessing between them", async () => {
    // A voucher settling two customers has two counterparties. Naming the first
    // would be this form inventing the answer, and the paper would then credit
    // one customer for the other's money. The document refuses to print, naming
    // the particular it lacks — which is the honest outcome.
    const base = createMockClient({ seed: false });
    await seedBooks(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F6" });

    await fillSingleEntry("HDFC Bank", [
      ["Acme Traders", "5000"],
      ["Bharat Stores", "3000"],
    ]);
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.postReceiptVoucher).toHaveBeenCalledTimes(1));

    const [, req] = spies.postReceiptVoucher!.mock.calls[0]!;
    expect((req as { party: string }).party).toBe("");
    expect((req as { party_ledger_id?: string }).party_ledger_id).toBeUndefined();
    // The voucher itself is untouched — 8,000 into the bank, both customers
    // credited. Only the claim about who it is FOR is withheld.
    const { debits, credits } = await sidesByName(
      client,
      (req as { postings: PostingRequest[] }).postings,
    );
    expect(debits).toEqual([["HDFC Bank", "8000.0000"]]);
    expect(credits).toEqual([
      ["Acme Traders", "5000.0000"],
      ["Bharat Stores", "3000.0000"],
    ]);
  });

  it("⛔⛔ the ALTER round trip keeps the BANK in the Account field and both legs on the voucher", async () => {
    // ⛔ THE TEST THAT STOPS THE FIX BECOMING A CATASTROPHE. `party` no longer
    // names the account, so a form that hydrated its Account field from the
    // stored party would open this receipt showing "Acme Traders" as the bank
    // and the next Ctrl+A would post Dr Acme / Cr Acme — the bank silently gone
    // from a voucher that still balances. The account is read from the
    // voucher's own primary-side POSTING instead (accountLegFromView).
    const base = createMockClient({ seed: false });
    await seedBooks(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F6" });
    await fillSingleEntry("HDFC Bank", [["Acme Traders", "5000"]]);
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.postReceiptVoucher).toHaveBeenCalledTimes(1));
    const saved = await spies.postReceiptVoucher!.mock.results[0]!.value;

    // Day Book -> the row -> Enter, the way an operator drills. ⛔ Alt+G is
    // pressed as the PHYSICAL key with the character a Mac actually sends:
    // Option composes, so a real Mac emits "©" and never "g".
    fireEvent.keyDown(app, { key: "Escape" });
    fireEvent.keyDown(app, { key: "©", code: "KeyG", altKey: true });
    fireEvent.mouseDown(screen.getByText("Day Book"));
    fireEvent.keyDown(app, { key: "Enter" });
    await waitFor(() => expect(screen.getByText(saved.voucher_no)).toBeTruthy());
    fireEvent.keyDown(app, { key: "Enter" });
    await waitFor(() => expect(spies.getVoucher).toHaveBeenCalled());

    // ⛔ THE ACCOUNT FIELD SHOWS THE BANK — visible on the form, which is where
    // an operator would have seen the corruption coming.
    await waitFor(() =>
      expect(
        (screen.getByRole("combobox", { name: "Account" }) as HTMLInputElement).value,
      ).toBe("HDFC Bank"),
    );

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.alterReceiptVoucher).toHaveBeenCalledTimes(1));
    const [, , req] = spies.alterReceiptVoucher!.mock.calls[0]!;
    const { debits, credits } = await sidesByName(
      client,
      (req as { postings: PostingRequest[] }).postings,
    );
    // BOTH LEGS, BY NAME. A merged voucher would read Dr Acme / Cr Acme here
    // and would balance.
    expect(debits).toEqual([["HDFC Bank", "5000.0000"]]);
    expect(credits).toEqual([["Acme Traders", "5000.0000"]]);
    // ...and the counterparty survives the round trip.
    const acme = (await client.listLedgers("co-1")).ledgers.find(
      (l) => l.name === "Acme Traders",
    )!;
    expect((req as { party_ledger_id?: string }).party_ledger_id).toBe(acme.id);
  });
});
