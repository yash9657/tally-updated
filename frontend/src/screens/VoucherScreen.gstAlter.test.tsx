import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import {
  render,
  screen,
  fireEvent,
  cleanup,
  waitFor,
  act,
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

/**
 * ⛔ LET THE ACCEPT CHAIN SETTLE, so "nothing was posted" is a MEASUREMENT and
 * not a race. A `waitFor` cannot be used to assert an ABSENCE — it returns on
 * its first successful poll, before the request it is supposed to prove did not
 * happen would have happened. This drains microtasks across three macrotask
 * turns, which is comfortably more than the chain needs: `createMockClient`
 * resolves without timers, and the duplicate POST this test is about lands on
 * the first turn (measured — reverting the fix turns the assertion below red
 * with `2`, the number the browser measured in psql).
 */
async function settle() {
  for (let i = 0; i < 3; i++) {
    // eslint-disable-next-line no-await-in-loop
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
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

/**
 * ===========================================================================
 * ⛔⛔ REGISTER ROW D82 — THE HSN REFUSAL ON THE ALTER PATH IS **WORKING AS
 * INTENDED**, AND THIS SUITE EXISTS SO NOBODY QUIETLY RELAXES IT.
 * ===========================================================================
 *
 * The row was registered as a defect: an old sales invoice stored WITHOUT an
 * HSN cannot be re-saved without keying one, and that is a new refusal biting
 * existing data. It was put to the user on 2026-08-21 with a recommendation,
 * and the user's decision is that it is CORRECT:
 *
 *   the invoice is UNPRINTABLE without an HSN (CGST Rules 2017, Rule 46(g)),
 *   so keying the code is the FIX rather than a hoop.
 *
 * ⛔ SO THE WORK HERE IS A PIN, NOT A CHANGE. `HSN_REQUIRED_CLASSES` stays
 * exactly as it is. What this file adds is the thing that was missing: nothing
 * asserted the refusal REACHES an operator on an ALTER, and nothing asserted it
 * READS as a reason. A later session finding this row in the register and
 * "fixing" it would be reversing a decision the user made — so the SENTENCE is
 * pinned, not just the behaviour.
 *
 * ⚠️ THE ALTER PATH IS THE ONLY WAY TO GET HERE. A blank-HSN sale cannot be
 * CREATED through this form at all — the same refusal fires (that is D60) — so
 * the legacy invoice below is seeded through the client, which is exactly how a
 * real one arrives: an import, or a build that predates the refusal.
 */
describe("⛔ D82: the HSN refusal on an ALTER — working as intended, pinned", () => {
  /** A sales invoice on the books with NO HSN. Unreachable through the form. */
  async function seedBlankHsnInvoice(base: CoredClient) {
    return await base.postSalesVoucher("co-1", {
      voucher_date: "2026-06-01",
      narration: "legacy invoice, imported without an HSN",
      party: "Acme Traders",
      postings: [],
      gst: {
        place_of_supply_state_code: "29",
        reverse_charge: false,
        lines: [{ taxable_value: "10000.0000", gst_rate_pct: "18", hsn: "" }],
      },
    } as VoucherRequest);
  }

  /**
   * ⚠️ SCOPED. There are TWO `role="status"` spans on this screen, and a bare
   * getByRole("status") can throw on the ambiguity rather than fail on the text.
   */
  const statusText = () =>
    screen.getAllByRole("status").map((e) => e.textContent ?? "").join(" | ");

  it("the refusal REACHES the operator on the alter, and says WHY", async () => {
    const base = createMockClient({ seed: false });
    await seedParty(base);
    const { client, spies } = spyClient(base);
    const posted = await seedBlankHsnInvoice(base);

    renderShell(client);
    const app = screen.getByRole("application");
    await drill(app, posted.voucher_no);
    await waitFor(() => expect(spies.getVoucher).toHaveBeenCalled());

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(statusText()).toMatch(/HSN \/ SAC is required/));
    // ⛔ THE THREE THINGS THE USER'S DECISION IS ABOUT. Not "invalid input" —
    // the operator has to learn that the document is UNPRINTABLE and that the
    // remedy is to key the code, or the refusal reads as an arbitrary hoop.
    const said = statusText();
    expect(said).toMatch(/Rule 46\(g\)/);
    expect(said).toMatch(/cannot be printed/);
    expect(said).toMatch(/Key the code for what was supplied/);

    // ⛔ AND NOTHING WAS SENT. A refusal that still posts is not a refusal.
    expect(spies.alterSalesVoucher).not.toHaveBeenCalled();
    const kept = await base.getVoucher("co-1", posted.voucher_id);
    expect(kept.gst_lines?.[0]?.hsn).toBe("");
  });

  it("⛔ THE REMEDY IS ON THE SAME SCREEN — the field is there, empty and keyable", async () => {
    // A refusal whose remedy is not reachable from the keyboard would make this
    // a real defect rather than a decision. It is the item-invoice path that has
    // that shape (its HSN lives on the stock master), and that one is DISCLOSED
    // instead of refused — see item-hsn-missing.
    const base = createMockClient({ seed: false });
    await seedParty(base);
    const { client, spies } = spyClient(base);
    const posted = await seedBlankHsnInvoice(base);
    renderShell(client);
    const app = screen.getByRole("application");
    await drill(app, posted.voucher_no);
    await waitFor(() => expect(spies.getVoucher).toHaveBeenCalled());

    const hsn = screen.getByTestId("gst-hsn") as HTMLInputElement;
    expect(hsn.value).toBe("");
    fireEvent.change(hsn, { target: { value: "7326" } });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() => expect(spies.alterSalesVoucher).toHaveBeenCalledTimes(1));
    const kept = await base.getVoucher("co-1", posted.voucher_id);
    expect(kept.gst_lines?.[0]?.hsn).toBe("7326");
    // ⛔ AND THE RATE SURVIVED THE ROUND TRIP TOO, so this control cannot pass
    // by re-deriving the invoice at 0% — the defect this whole file was written
    // for.
    expect(kept.gst_lines?.[0]?.gst_rate_pct).toBe("18.0000");
  });

  it("⛔ CONTROL: a PURCHASE with no HSN alters freely — the rule is per class", async () => {
    // HSN_REQUIRED_CLASSES is ["sales"], and it is deliberately not every GST
    // class: Rule 46(g) governs the invoice a company ISSUES. A test that only
    // ever saw the refusal could not tell "required on a sale" from "required
    // always", and the second would refuse re-saving every supplier bill.
    const base = createMockClient({ seed: false });
    await base.createLedger("co-1", {
      name: "Steel Suppliers",
      group_name: "Sundry Creditors",
      state_code: "29",
    });
    const { client, spies } = spyClient(base);
    const posted = await base.postPurchaseVoucher("co-1", {
      voucher_date: "2026-06-01",
      narration: "supplier bill, no HSN",
      party: "Steel Suppliers",
      postings: [],
      gst: {
        place_of_supply_state_code: "29",
        reverse_charge: false,
        lines: [{ taxable_value: "10000.0000", gst_rate_pct: "18", hsn: "" }],
      },
    } as VoucherRequest);

    renderShell(client);
    const app = screen.getByRole("application");
    await drill(app, posted.voucher_no);
    await waitFor(() => expect(spies.getVoucher).toHaveBeenCalled());
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.alterPurchaseVoucher).toHaveBeenCalledTimes(1));
  });
});

/**
 * ===========================================================================
 * ⛔⛔ REGISTER ROW D89 — THE SEQUENCE THE D82 SUITE ABOVE NEVER PRESSED:
 * **CTRL+A TWICE.**
 * ===========================================================================
 *
 * Every test in the D82 block above presses Ctrl+A exactly ONCE. One of them
 * proves the refusal reaches the operator; another proves that keying the code
 * lets the alter through. Neither does BOTH, in one frame, in the order an
 * operator actually meets them — and that gap is the whole defect:
 *
 *   Ctrl+A -> refused CLIENT-SIDE (nothing sent) -> key the HSN the refusal
 *   asked for -> Ctrl+A -> **postSalesVoucher**, a SECOND invoice on the books,
 *   status line "Saved", and the original's `hsn` still empty.
 *
 * `navMachine`'s `accept` reducer replaced the alter frame with a fresh CREATE
 * frame on every Ctrl+A — it is pure, so it cannot know the write was refused —
 * which dropped the loaded `voucher_id` that routes the PUT while the form kept
 * the loaded voucher's values on screen. See the D89 block in
 * `keystroke/phase1.test.ts` for the reducer half and the reasoning.
 *
 * ⛔ THIS IS THE LESSON, NOT JUST THE FIX. The D82 suite went green on its
 * first run, and it was green *through* this defect, because all three of its
 * tests share one shape: a single accept on a freshly drilled frame. Found by
 * DRIVING it in a browser (L14), which is the only place the second Ctrl+A ever
 * got pressed.
 */
describe("⛔ D89: the refusal's own remedy must not mint a second invoice", () => {
  const statusText = () =>
    screen.getAllByRole("status").map((e) => e.textContent ?? "").join(" | ");

  async function seedBlankHsnInvoice(base: CoredClient) {
    return await base.postSalesVoucher("co-1", {
      voucher_date: "2026-06-01",
      narration: "legacy invoice, imported without an HSN",
      party: "Acme Traders",
      postings: [],
      gst: {
        place_of_supply_state_code: "29",
        reverse_charge: false,
        lines: [{ taxable_value: "10000.0000", gst_rate_pct: "18", hsn: "" }],
      },
    } as VoucherRequest);
  }

  it("⛔ refuse, key the code the refusal asked for, accept: it ALTERS — it does not POST a twin", async () => {
    const base = createMockClient({ seed: false });
    await seedParty(base);
    const { client, spies } = spyClient(base);
    const posted = await seedBlankHsnInvoice(base);
    const postsBefore = spies.postSalesVoucher!.mock.calls.length;

    renderShell(client);
    const app = screen.getByRole("application");
    await drill(app, posted.voucher_no);
    await waitFor(() => expect(spies.getVoucher).toHaveBeenCalled());

    // ---- 1. the refusal, exactly as D82 pins it
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(statusText()).toMatch(/HSN \/ SAC is required/));
    expect(spies.alterSalesVoucher).not.toHaveBeenCalled();

    // ---- 2. ⛔ THE FRAME MUST STILL BE THE ONE IT WAS ALTERING. This is the
    // assertion the defect failed, and it is checked on the SCREEN's own
    // heading rather than on the machine, because the heading is the only thing
    // that told an operator anything: the form kept showing the loaded
    // voucher's 10,000, so a frame that had silently become a create was
    // indistinguishable from the alter it replaced.
    expect(screen.getByRole("heading", { level: 1 }).textContent).toMatch(
      /\(Alteration\)/,
    );

    // ---- 3. the remedy the refusal prescribed
    const hsn = screen.getByTestId("gst-hsn") as HTMLInputElement;
    fireEvent.change(hsn, { target: { value: "7326" } });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    // ---- 4. ⛔ IT WENT THROUGH AS AN ALTER, AND NOTHING WAS CREATED.
    await waitFor(() =>
      expect(spies.alterSalesVoucher).toHaveBeenCalledTimes(1),
    );
    expect(spies.postSalesVoucher!.mock.calls.length).toBe(postsBefore);
    expect(spies.alterSalesVoucher!.mock.calls[0]![1]).toBe(posted.voucher_id);

    // ---- 5. ⛔ THE BOOKS. The ORIGINAL invoice carries the code now — read
    // back through the store, not off the screen, because the screen was the
    // thing that said "Saved" while the books held two invoices and neither had
    // an HSN.
    const kept = await base.getVoucher("co-1", posted.voucher_id);
    expect(kept.gst_lines?.[0]?.hsn).toBe("7326");
    expect(kept.gst_lines?.[0]?.gst_rate_pct).toBe("18.0000");
  });

  it("⛔ CONTROL: two accepts on a CREATE frame really do key two invoices", async () => {
    // The fix must not turn bulk entry into a re-save loop. Tally's F8 is
    // key-invoice-Ctrl+A-key-the-next, and a guard written as "never accept the
    // same frame twice" would satisfy the test above and break exactly that.
    const base = createMockClient({ seed: false });
    await seedParty(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    await keySale(app, "10000", "18", "7326");
    await waitFor(() =>
      expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1),
    );
    await keySale(app, "20000", "18", "7326");
    await waitFor(() =>
      expect(spies.postSalesVoucher).toHaveBeenCalledTimes(2),
    );
    expect(spies.alterSalesVoucher).not.toHaveBeenCalled();
  });

  // -------------------------------------------------------------------------
  // ⛔⛔ D108 — AND IT IS THE CONTROL ABOVE THAT MAKES THIS ONE READABLE.
  //
  // The control proves a RE-KEYED second accept posts a second invoice, which
  // is bulk entry and is correct. It says nothing about an UNTOUCHED one,
  // because `keySale` sets every field explicitly and so is green either way —
  // and that gap is the whole defect. Driven in a browser 2026-08-31 on scratch
  // book 7c3ab7e9: F8, Acme Traders, taxable 10000, rate 18, HSN 7326, Ctrl+A,
  // Ctrl+A, **nothing re-keyed between the presses** -> Sales No. 4 AND No. 5,
  // each 10,000.00 + 900.00 CGST + 900.00 SGST. One sale keyed, two tax
  // invoices on the books, the customer billed twice and the GST output
  // liability doubled — and there is no delete path in this product, so the
  // recovery is a hand-keyed credit note after somebody notices.
  //
  // ⛔ THE TWO CASES ARE PINNED SIDE BY SIDE ON PURPOSE. A fix written as
  // "never accept the same frame twice" turns this test green and the control
  // above red; a fix that clears the form on a successful create satisfies both.
  // -------------------------------------------------------------------------
  it("⛔ D108: an UNCHANGED second Ctrl+A posts NOTHING — one sale keyed is one invoice", async () => {
    const base = createMockClient({ seed: false });
    await seedParty(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    await keySale(app, "10000", "18", "7326");
    await waitFor(() =>
      expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1),
    );

    // ---- 1. THE RECEIPT IS ON SCREEN, and it is the SERVER's number. This is
    // the half that must SURVIVE the clear: an operator shown nothing at all
    // after a save has less confirmation than the defect gave them.
    await waitFor(() =>
      expect(screen.getByTestId("voucher-no").textContent).toMatch(/No: \S+/),
    );

    // ---- 2. ⛔⛔ THE PRESS THAT USED TO BILL THE CUSTOMER TWICE. Nothing is
    // re-keyed between the presses; this is one keystroke and nothing else.
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await settle();
    // ⛔ NOT "at most 2" — EXACTLY 1, and this is the assertion that goes red
    // with the number the browser measured. The oracle there was the voucher
    // count in psql, and it rose by one per keyed sale.
    expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1);
    expect(spies.alterSalesVoucher).not.toHaveBeenCalled();
    // The refusal names the field the blank form is missing, which is the
    // honest answer to Ctrl+A on an empty voucher — and is D107's shape too.
    expect(screen.getByRole("status").textContent).toMatch(/is required/);

    // ---- 3. ⛔ AND HERE IS WHY. `navMachine`'s accept reducer has always
    // replaced the create frame with a FRESH one — "Ctrl+A accepts the voucher
    // and re-opens a fresh form for bulk entry", which is what
    // corpora/keystroke/voucher-ctrl-a-accepts.json specifies. The form is React
    // component state, so replacing the frame object never remounted it and
    // every keyed value survived. The frame was fresh; the form was not.
    expect(
      (screen.getByTestId("gst-taxable") as HTMLInputElement).value,
    ).toBe("0.0000");
    expect((screen.getByTestId("gst-rate") as HTMLInputElement).value).toBe("");
    expect((screen.getByTestId("gst-hsn") as HTMLInputElement).value).toBe("");
    // ⚠️ THE PARTY BOX IS READ OFF THE DOM, NOT OFF THE FORM. SelectionList is a
    // type-ahead holding its own `query`, so a form field cleared underneath it
    // can still SHOW the previous party — which would leave the operator keying
    // the next invoice under the last customer's name.
    expect(
      (screen.getByRole("combobox", { name: "Party A/c Name" }) as HTMLInputElement)
        .value,
    ).toBe("");
  });

  // -------------------------------------------------------------------------
  // ⛔ THE DATE SURVIVES THE CLEAR, AND THAT IS A DECISION, NOT AN OVERSIGHT.
  //
  // `emptyForm()` resets the date to today(), which is right for Alt+X (a
  // cancelled voucher has no date) and WRONG here: Tally keeps the date for the
  // next voucher in a run, and a shopkeeper keying Monday's invoices on
  // Wednesday would otherwise re-type the date on every one of them. The entry
  // MODE survives for the same reason and already did — it lives in
  // ctx.entryModeByType because a fresh frame carries no config (a run of
  // double-entry payments used to revert to single entry after every save).
  // -------------------------------------------------------------------------
  it("⛔ D108: the keyed DATE survives the clear, so a back-dated run stays back-dated", async () => {
    const base = createMockClient({ seed: false });
    await seedParty(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F8" });
    fireEvent.change(await screen.findByTestId("voucher-date"), {
      target: { value: "2026-01-15" },
    });
    fireEvent.mouseDown(await screen.findByText("Acme Traders"));
    fireEvent.change(screen.getByTestId("gst-taxable"), {
      target: { value: "10000" },
    });
    fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });
    fireEvent.change(screen.getByTestId("gst-hsn"), { target: { value: "7326" } });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() =>
      expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1),
    );
    const [, req] = spies.postSalesVoucher!.mock.calls[0]!;
    expect((req as { voucher_date: string }).voucher_date).toBe("2026-01-15");

    // The next voucher in the run opens on the SAME day.
    await waitFor(() =>
      expect(
        (screen.getByTestId("voucher-date") as HTMLInputElement).value,
      ).toBe("2026-01-15"),
    );
  });

  // -------------------------------------------------------------------------
  // ⛔⛔ AND NOT ON AN ALTER. THIS IS D89's GUARD AND IT MUST NOT BE WEAKENED.
  //
  // The success handler is shared between the two modes. On an ALTER the
  // machine deliberately leaves the frame in place — a second Ctrl+A re-PUTs
  // the SAME voucher_id, which is harmless — so clearing the form there would
  // blank a voucher the operator is still editing, on a screen whose heading
  // says "(Alteration)". The clear is conditioned on the edit context CAPTURED
  // SYNCHRONOUSLY before the request, never on a value re-read after the
  // promise resolves.
  // -------------------------------------------------------------------------
  it("⛔ CONTROL: a saved ALTER keeps its form — the voucher is still open", async () => {
    const base = createMockClient({ seed: false });
    await seedParty(base);
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    await keySale(app, "10000", "18", "7326");
    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const posted = await spies.postSalesVoucher!.mock.results[0]!.value;

    await drill(app, posted.voucher_no);
    await waitFor(() =>
      expect((screen.getByTestId("gst-rate") as HTMLInputElement).value).toBe(
        "18.0000",
      ),
    );
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(spies.alterSalesVoucher).toHaveBeenCalledTimes(1),
    );
    await settle();

    // The voucher is STILL ON THE FORM, and the frame is still the one it was
    // altering.
    expect(screen.getByRole("heading", { level: 1 }).textContent).toMatch(
      /\(Alteration\)/,
    );
    expect(
      (screen.getByTestId("gst-taxable") as HTMLInputElement).value,
    ).toBe("10000.0000");
    expect((screen.getByTestId("gst-rate") as HTMLInputElement).value).toBe(
      "18.0000",
    );
  });
});
