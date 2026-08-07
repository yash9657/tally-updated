import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import {
  render,
  screen,
  fireEvent,
  cleanup,
  waitFor,
} from "@testing-library/react";
import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient } from "../api";
import type { CoredClient, GSTLineRequest, VoucherRequest } from "../api";
import { addMoney } from "./vouchers/money";
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
 * ⛔⛔ ALTERING AN ACCOUNTING-MODE GST INVOICE USED TO STRIP ITS TAX.
 * ===========================================================================
 *
 * `voucher_gst_line` (migration 0016) is keyed on `voucher_version_id`, so an
 * ALTER RE-STATES it. It was never on §4.3's list of tables to check — not on
 * the cleared side, not on the unchecked side — and it is the WORST member of
 * that class, because the field the round trip lost is an INPUT and not a
 * record.
 *
 * `taxable_value`, `cgst`, `sgst` and `igst` on the voucher view are what the
 * derivation PRODUCED. The RATE it was derived AT lives only in the GST lines,
 * and **no rate is recoverable from the amounts** — an exempt line and a
 * nil-rated line both produce zero tax. So a form rebuilt from the GET had no
 * rate to put back, and the ordinary round trip — open an invoice, fix the
 * narration, save — re-derived the tax AT 0%:
 *
 *     an 11,800 invoice became 10,000, the version's GST columns went to zero,
 *     and the supply left both the GSTR-1 b2b section and the table-12 HSN
 *     summary.
 *
 * Nothing refused it, because a supply with no tax is a legal state.
 *
 * ⚠️ THE SERVER HALF (the GET now emitting `gst_lines`) AND THE MOCK HALF are
 * pinned elsewhere — `backend/internal/server/voucher_readback_audit_test.go`
 * and `src/api/gstLineReadback.test.ts`. **Neither of them touches a screen**,
 * and the whole lesson of §4.3 is that emitting is necessary and NOT
 * SUFFICIENT: `bill_allocation` was emitted by the GET for the entire life of
 * the feature while no form sent it back. THIS file is the other half — it
 * drives the real shell, because that is the only thing that proves a FORM
 * rebuilt from that response sends the rate back.
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

async function seedParty(base: CoredClient) {
  await base.createLedger("co-1", {
    name: "Acme Traders",
    group_name: "Sundry Debtors",
    state_code: "29",
  });
}

/** Key an accounting-mode GST sale and return the posted response. */
async function keySale(app: HTMLElement, taxable: string, rate: string, hsn: string) {
  fireEvent.keyDown(app, { key: "F8" });
  fireEvent.mouseDown(await screen.findByText("Acme Traders"));
  fireEvent.change(screen.getByTestId("gst-taxable"), {
    target: { value: taxable },
  });
  fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: rate } });
  const hsnField = screen.queryByTestId("gst-hsn");
  if (hsnField) fireEvent.change(hsnField, { target: { value: hsn } });
  fireEvent.keyDown(app, { key: "a", ctrlKey: true });
}

/** Day Book -> the row -> Enter, the way an operator drills. */
async function drill(app: HTMLElement, voucherNo: string) {
  fireEvent.keyDown(app, { key: "Escape" });
  fireEvent.keyDown(app, { key: "g", altKey: true });
  fireEvent.mouseDown(screen.getByText("Day Book"));
  fireEvent.keyDown(app, { key: "Enter" });
  await waitFor(() => expect(screen.getByText(voucherNo)).toBeTruthy());
  fireEvent.keyDown(app, { key: "Enter" });
}

describe("the RATE survives an alter of an accounting-mode GST invoice", () => {
  it("re-sends through the FORM the rate the GET returned", async () => {
    const base = createMockClient({ seed: false });
    await seedParty(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    await keySale(app, "10000", "18", "7326");
    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const posted = await spies.postSalesVoucher!.mock.results[0]!.value;
    // The invoice really did carry tax before the alter.
    expect(posted.cgst).toBe("900.0000");
    expect(posted.sgst).toBe("900.0000");

    await drill(app, posted.voucher_no);
    await waitFor(() => expect(spies.getVoucher).toHaveBeenCalled());

    // ⛔ THE HYDRATION IS VISIBLE ON THE FORM. This is the assertion the old
    // build failed: the rate field came back EMPTY, and the operator had no way
    // to know the next Ctrl+A would zero the tax.
    // ⚠️ "18.0000", not "18": the rate is stored and returned fixed-4 like
    // every other decimal on this wire. The form shows what the server holds.
    await waitFor(() =>
      expect((screen.getByTestId("gst-rate") as HTMLInputElement).value).toBe(
        "18.0000",
      ),
    );

    // Edit ONLY the narration-adjacent state and save — the ordinary round trip.
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(spies.alterSalesVoucher).toHaveBeenCalledTimes(1),
    );

    const [, , req] = spies.alterSalesVoucher!.mock.calls[0]!;
    const lines = (req as VoucherRequest).gst?.lines as GSTLineRequest[];
    expect(lines).toHaveLength(1);
    // ⛔ THE RATE WENT BACK. Before the fix this was "" and the server derived
    // an 18% invoice's tax at 0%.
    expect(lines[0]!.gst_rate_pct).toBe("18.0000");
    expect(lines[0]!.taxable_value).toBe("10000.0000");

    // ...and the STORE still holds the tax afterwards.
    const kept = await base.getVoucher("co-1", posted.voucher_id);
    expect(kept.cgst).toBe("900.0000");
    expect(kept.sgst).toBe("900.0000");
    expect(kept.taxable_value).toBe("10000.0000");
  });

  /**
   * ⛔ THE INVERSE CONTROL, and without it the test above proves nothing.
   *
   * "The tax survives an alter" is equally consistent with the tax being IMMUNE
   * to an alter — in which case the hydration is decoration. This establishes
   * that the behaviour the test above depends on is genuinely dangerous: an
   * alter that omits the rate really does destroy the tax.
   *
   * ⚠️ It also names the exact figure the defect produced in the product:
   * an 11,800 invoice becomes 10,000.
   */
  it("⛔ CONTROL: an alter sent WITHOUT the rate re-derives the tax at 0%", async () => {
    const base = createMockClient({ seed: false });
    await seedParty(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    // Keyed through the FORM, so the voucher is the shape the product makes.
    await keySale(app, "10000", "18", "7326");
    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const posted = await spies.postSalesVoucher!.mock.results[0]!.value;

    const before = await base.getVoucher("co-1", posted.voucher_id);
    const taxOf = (v: { cgst: string; sgst: string; igst: string }) =>
      // Summed as fixed-4 STRINGS through the exact adder — never Number().
      addMoney(addMoney(v.cgst, v.sgst), v.igst);
    expect(taxOf(before)).toBe("1800.0000");
    expect(before.gst_lines?.[0]?.gst_rate_pct).toBe("18.0000");

    // The body a form with NO hydrated rate builds: same taxable value, rate
    // blank -> "0". This is exactly what the product sent before the fix.
    await base.alterSalesVoucher("co-1", posted.voucher_id, {
      voucher_date: before.voucher_date,
      voucher_no: before.voucher_no,
      narration: "typo fixed",
      party: before.party,
      party_ledger_id: before.party_ledger_id,
      postings: [],
      gst: {
        place_of_supply_state_code: before.place_of_supply,
        reverse_charge: false,
        lines: [
          { taxable_value: "10000.0000", gst_rate_pct: "0", hsn: "7326" },
        ],
      },
    } as VoucherRequest);

    const after = await base.getVoucher("co-1", posted.voucher_id);
    // ⛔ 1,800 OF TAX DESTROYED BY A NARRATION EDIT, and the taxable value it
    // was charged on is still sitting there — which is what made it invisible:
    // the invoice still looks like an invoice, for 10,000 instead of 11,800.
    expect(taxOf(after)).toBe("0.0000");
    expect(after.taxable_value).toBe("10000.0000");
    expect(after.gst_lines?.[0]?.gst_rate_pct).toBe("0.0000");
  });
});

describe("a MULTI-RATE voucher is refused rather than silently collapsed", () => {
  /**
   * ⛔ THE DEFECT THE FIRST FIX WOULD HAVE INTRODUCED, caught by an adversarial
   * review of it: `view.gst_lines?.[0]` takes only the FIRST line.
   *
   * That is correct for the one-rate accounting form and silently wrong for any
   * multi-rate voucher — which the item path generates and a Tally XML import
   * can bring in. Rebuilt through this form, such a voucher keeps line 1 and
   * DELETES the rest, re-deriving the whole tax from what survived. Same
   * signature as the defect being fixed: it balances, the server derives a
   * consistent tax, and nothing refuses it.
   *
   * Building a multi-rate editor here would be a real feature. Collapsing the
   * voucher is a data-loss bug wearing a feature's clothes, so the form refuses
   * and names the mode that CAN hold it.
   */
  it("refuses to save, naming the line count and the mode that can hold it", async () => {
    const base = createMockClient({ seed: false });
    await seedParty(base);
    const { client, spies } = spyClient(base);

    // A two-rate accounting-mode voucher — reachable by import, not by this form.
    const posted = await base.postSalesVoucher("co-1", {
      voucher_date: "2026-06-01",
      narration: "two rates",
      party: "Acme Traders",
      postings: [],
      gst: {
        place_of_supply_state_code: "29",
        reverse_charge: false,
        lines: [
          { taxable_value: "10000.0000", gst_rate_pct: "18", hsn: "7326" },
          { taxable_value: "5000.0000", gst_rate_pct: "5", hsn: "1006" },
        ],
      },
    } as VoucherRequest);

    renderShell(client);
    const app = screen.getByRole("application");
    await drill(app, posted.voucher_no);
    await waitFor(() => expect(spies.getVoucher).toHaveBeenCalled());

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toContain("2 GST lines"),
    );
    expect(screen.getByRole("status").textContent).toContain("Item Invoice");
    // ⛔ AND NOTHING WAS SENT. A refusal that still posts is not a refusal.
    expect(spies.alterSalesVoucher).not.toHaveBeenCalled();

    // The store is untouched: both lines, both rates, still there.
    const kept = await base.getVoucher("co-1", posted.voucher_id);
    expect(kept.gst_lines).toHaveLength(2);
  });

  it("⛔ CONTROL: a ONE-line voucher is NOT refused", async () => {
    // Without this, a guard that refused every alter would pass the test above
    // and break the ordinary case — which is most invoices in the product.
    const base = createMockClient({ seed: false });
    await seedParty(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    await keySale(app, "10000", "18", "7326");
    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const posted = await spies.postSalesVoucher!.mock.results[0]!.value;

    await drill(app, posted.voucher_no);
    await waitFor(() => expect(spies.getVoucher).toHaveBeenCalled());
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() =>
      expect(spies.alterSalesVoucher).toHaveBeenCalledTimes(1),
    );
  });
});
