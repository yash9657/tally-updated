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

  it("on a SALES voucher it offers the invoice modes, each marked not built", async () => {
    // The honest answer, matching the keymap's treatment of unavailable combos:
    // a key that silently does nothing is what makes a keyboard user distrust
    // the app. Picking one dismisses the overlay and changes nothing.
    renderShell(spyClient().client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" });
    fireEvent.keyDown(app, { key: "h", ctrlKey: true });

    expect(await screen.findByTestId("change-mode-overlay")).toBeTruthy();
    expect(screen.getByText(/Item Invoice — not built/)).toBeTruthy();
    expect(screen.queryByText(/^Single Entry/)).toBeNull();

    fireEvent.mouseDown(screen.getByText(/As Voucher — not built/));
    await waitFor(() => expect(screen.queryByTestId("change-mode-overlay")).toBeNull());
    expect(screen.queryByTestId("single-entry-panel")).toBeNull();
  });
});
