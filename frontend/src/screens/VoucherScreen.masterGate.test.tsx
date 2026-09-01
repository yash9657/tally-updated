import { readFileSync } from "node:fs";
import { join } from "node:path";
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

/**
 * ===========================================================================
 * ⛔⛔ THE STRUCTURAL HALF: A **NEW** BUILDER MUST NOT BE ABLE TO RE-OPEN THIS.
 * ===========================================================================
 *
 * Every test in this file drives one keystroke on one form, and four green
 * builders is not the same statement as "the class is closed". This repo has
 * paid for that difference more than once: the back-dated cutoff was declared
 * fixed FOUR times, each round asserting a LIST of hostile inputs, and the next
 * bypass was always off-list — what finally held was asserting the PROPERTY plus
 * a walk that fails when something new goes unaccounted for.
 *
 * So this is the walk. It reads VoucherScreen.tsx and asserts the ORDERING that
 * the gate is: inside every `build*Request`, the refusal comes before the first
 * `ensure*`. A fifth builder added tomorrow, or a new `ensureLedger` pasted at
 * the top of an existing one, fails HERE — with a message naming the line —
 * rather than in a browser six weeks later, as an account with no voucher
 * behind it.
 *
 * ⚠️ WHAT IT DOES NOT PROVE, SAID PLAINLY SO NOBODY LEANS ON IT FOR MORE: it is
 * a LINE-ORDER check, not a scope check. It catches an `ensure*` that runs
 * before the gate — the shape D39 and D41 actually were — and it would not
 * catch one placed after the gate's closure has already returned. The behaviour
 * tests above are what cover that; this covers the ones nobody has written yet.
 */
describe("⛔ the master gate is STRUCTURAL, not four remembered call sites", () => {
  /** Comment and doc lines mention these helpers constantly; only CODE counts. */
  function isCode(line: string): boolean {
    const t = line.trim();
    return !(t.startsWith("*") || t.startsWith("//") || t.startsWith("/*"));
  }

  it("every build*Request refuses BEFORE its first ensure*, and none stands outside one", () => {
    const src = readFileSync(join(__dirname, "VoucherScreen.tsx"), "utf8");
    const lines = src.split("\n");

    const BUILDER = /^\s*const (build\w+) = useCallback\(/;
    // Where the last builder ends: the accept handler that dispatches to them.
    const END = /^\s*const onAccept = useCallback\(/;
    const ENSURE = /\bensure(Ledger|GstTaxLedgers)\(/;
    const GATE = /\bresolveMasters\(/;

    type Region = { name: string; start: number; end: number };
    const regions: Region[] = [];
    lines.forEach((line, i) => {
      const m = BUILDER.exec(line);
      if (m) regions.push({ name: m[1]!, start: i, end: lines.length });
      if (END.test(line) && regions.length > 0) {
        const last = regions[regions.length - 1]!;
        if (last.end === lines.length) last.end = i;
      }
    });
    for (let r = 0; r < regions.length - 1; r++) {
      regions[r]!.end = Math.min(regions[r]!.end, regions[r + 1]!.start);
    }
    // The premise: if this file is ever restructured so the builders are not
    // found, the walk must FAIL rather than pass vacuously — the exact way a
    // structural gate stops being one.
    expect(regions.map((r) => r.name).sort()).toEqual([
      "buildDoubleEntryRequest",
      "buildGstRequest",
      "buildItemRequest",
      "buildPlainRequest",
      "buildSingleEntryRequest",
    ]);

    // ⛔ NO `ensure*` MAY STAND OUTSIDE A BUILDER AT ALL. One in an effect, a
    // handler or a field's onChange would create a master with no write behind
    // it even pending — a strictly worse version of the same defect.
    const inside = (i: number) =>
      regions.some((r) => i >= r.start && i < r.end);
    const strays = lines
      .map((line, i) => ({ line, i }))
      .filter(({ line, i }) => isCode(line) && ENSURE.test(line) && !inside(i))
      .map(({ i }) => `VoucherScreen.tsx:${i + 1}`);
    expect(strays).toEqual([]);

    // ⛔ AND INSIDE EACH ONE, THE REFUSAL COMES FIRST. Reported per builder, by
    // NAME and LINE, because "false" would send the next reader hunting.
    const ungated: string[] = [];
    for (const r of regions) {
      const body = lines.slice(r.start, r.end);
      const firstEnsure = body.findIndex((l) => isCode(l) && ENSURE.test(l));
      if (firstEnsure === -1) continue; // buildPlainRequest creates nothing.
      const firstGate = body.findIndex((l) => isCode(l) && GATE.test(l));
      if (firstGate === -1 || firstGate > firstEnsure) {
        ungated.push(
          `${r.name}: ensure* at VoucherScreen.tsx:${r.start + firstEnsure + 1} ` +
            `is not behind resolveMasters`,
        );
      }
    }
    expect(ungated).toEqual([]);
  });
});

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
    //
    // ⛔⛔ THIS EXPECTATION USED TO INCLUDE "Purchases", AND IT WAS PINNING D79.
    // `storePlainPurchase` books its expense to **Consumables**, and the builder
    // ensured the orientation's CANONICAL base on every accept — so this alter
    // minted a "Purchases" ledger and RE-POINTED the voucher's expense head to
    // it. The test wrote that down as the expected outcome. Driving D41 in a
    // browser found the same thing in real books (`Dr Raw Material Purchases`
    // became `Dr Purchases` on an alter that edited nothing), and the fix
    // hydrates the stored head instead. So the canonical base is NOT created
    // here any more, and that is the point.
    expect(createdLedgerNames(spies).sort()).toEqual([
      "Input CGST",
      "Input IGST",
      "Input SGST",
    ]);
    // ⛔ AND THE HEAD THE VOUCHER WAS BOOKED TO IS THE ONE IT KEEPS.
    const debitLedgers = (req as { postings: PostingRequest[] }).postings
      .filter((p) => p.dr_cr === "DR")
      .map((p) => p.ledger_id);
    const consumables = (await base.listLedgers("co-1")).ledgers.find(
      (l) => l.name === "Consumables",
    );
    expect(consumables).toBeTruthy();
    expect(debitLedgers).toContain(consumables!.id);
    // ⛔ AND THE THREE IT MINTED ARE NOT DISCLOSED, BECAUSE THE VOUCHER WAS
    // WRITTEN (D81). The orphan sentence is about masters with NO voucher against
    // them; saying it here would tell an operator whose alter succeeded that they
    // have three accounts to go and clean up. Minting is normal — orphaning is not.
    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toMatch(/Saved/),
    );
    expect(screen.getByRole("status").textContent).not.toMatch(/had already created/);
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

/**
 * ===========================================================================
 * ⛔⛔ THE RESIDUAL WINDOW THE GATE ITSELF SAYS IT CANNOT CLOSE — REGISTER D81.
 * ===========================================================================
 *
 * Everything above is about masters that must NEVER be created. This is about the
 * ones that legitimately ARE, for a voucher the SERVER then refuses: a back-dated
 * cutoff, an ACL denial, a duplicate number, negative stock. The gate's own header
 * says this window stays open and calls it NOT CLOSED, because a posting needs real
 * ledger uuids and cored has no DELETE /ledgers to compensate with.
 *
 * ⛔ SO THE ORACLE IS NOT "NO MASTER EXISTS" — IT IS "THE OPERATOR IS TOLD WHICH".
 * Measured at HEAD before the fix: a GST sales invoice refused for a closed period
 * left FOUR accounts on the chart (Output CGST, Output SGST, Output IGST, Domestic
 * Sales) and the status line read `Error: period is closed for back-dated entry`.
 * Four masters, named by nothing. The chart of accounts is what an auditor reads.
 *
 * ⚠️ THE DISCLOSURE IS FOOTED AGAINST A DIFFERENT SOURCE THAN THE ONE THAT WROTE
 * IT. `createdLedgerNames` reads the `createLedger` SPY — what actually went over
 * the wire — while the sentence is built from a chart diff inside the screen. Both
 * sides coming from one handler would prove only that the handler agrees with
 * itself (§2.2), and this defect is exactly a case where it could agree and lie.
 */
describe("⛔ D81: a refused accept NAMES the masters it had already minted", () => {
  /** A GST sales invoice that is complete, so only the SERVER can refuse it. */
  async function keyASaleableInvoice(app: HTMLElement, base: CoredClient) {
    await base.createLedger("co-1", {
      name: "Acme Traders",
      group_name: "Sundry Debtors",
      state_code: "29",
    });
    fireEvent.keyDown(app, { key: "F8" });
    await screen.findByTestId("gst-panel");
    await keyTheInvoice();
  }

  /**
   * The keying alone, on a sales form that is already open — the party is picked
   * from the chart rather than seeded, so this can be called a second time.
   * Needed because a successful CREATE now CLEARS the form (D108): an arm that
   * wants two accepts on the wire has to key two invoices, which is bulk entry.
   */
  async function keyTheInvoice() {
    fireEvent.mouseDown(await screen.findByText("Acme Traders"));
    fireEvent.change(screen.getByTestId("gst-taxable"), { target: { value: "10000" } });
    fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });
    fireEvent.change(screen.getByTestId("gst-pos"), { target: { value: "29" } });
    fireEvent.change(screen.getByTestId("gst-hsn"), { target: { value: "9983" } });
  }

  it("names ALL FOUR, keeps the server's own sentence first, and says a retry reuses them", async () => {
    const base = createMockClient({ seed: false });
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");
    await keyASaleableInvoice(app, base);
    spies.createLedger!.mockClear(); // the seeded party is not this accept's doing

    // The refusal only the server can make, arriving AFTER the four masters exist.
    spies.postSalesVoucher!.mockImplementationOnce(() =>
      Promise.reject(new Error("period is closed for back-dated entry")),
    );
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));

    const status = await waitFor(() => {
      const t = screen.getByRole("status").textContent ?? "";
      expect(t).toMatch(/had already created/);
      return t;
    });

    // ⛔ THE SERVER'S OWN REFUSAL IS STILL FIRST AND UNALTERED — it is the sentence
    // that tells the operator what to FIX. The orphan note is what they need after.
    expect(status.indexOf("period is closed")).toBeLessThan(
      status.indexOf("had already created"),
    );
    // ⛔ AND EVERY MINTED MASTER IS NAMED. Footed against the createLedger spy.
    const minted = createdLedgerNames(spies);
    expect(minted.sort()).toEqual([
      "Domestic Sales",
      "Output CGST",
      "Output IGST",
      "Output SGST",
    ]);
    for (const name of minted) expect(status).toContain(name);
    expect(status).toMatch(/4 new ledger masters/);
    // The two things the operator can act on: a retry is safe, and nothing here
    // can undo the accounts.
    expect(status).toMatch(/Retrying this accept REUSES them/);
    expect(status).toMatch(/no route that deletes a ledger/);
  });

  it("⛔ CONTROL: a refusal that minted NOTHING says nothing about masters", async () => {
    // Without this the fix is indistinguishable from pasting an orphan clause onto
    // every error the screen can show — which would name masters that do not exist
    // and send the operator hunting for accounts nobody created (§5.19: a
    // disclosure can be fully present and fully wrong).
    const base = createMockClient({ seed: false });
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");
    await keyASaleableInvoice(app, base);

    // Post once so all four masters EXIST, then refuse the second post. The second
    // accept mints nothing: ensureLedger resolves them off the chart.
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.getByRole("status").textContent).toMatch(/Saved/));

    // ⛔ THE INVOICE IS RE-KEYED, AND THAT IS NOT BOILERPLATE — IT IS D108.
    // A successful CREATE now clears the form, because a second Ctrl+A on a
    // voucher already on the books used to post it a SECOND time (one sale
    // keyed, two tax invoices). This arm needs a second accept that reaches the
    // wire, so it has to key a second invoice — which is bulk entry, and is
    // exactly what an operator does. The scenario is unchanged: the four
    // masters already exist, so this accept mints nothing.
    await keyTheInvoice();

    spies.createLedger!.mockClear();
    spies.postSalesVoucher!.mockImplementationOnce(() =>
      Promise.reject(new Error("voucher number 1 already exists")),
    );
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(2));
    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toMatch(/already exists/),
    );

    expect(createdLedgerNames(spies)).toEqual([]);
    const status = screen.getByRole("status").textContent ?? "";
    expect(status).not.toMatch(/had already created/);
    expect(status).not.toMatch(/ledger master/);
  });

  it("⛔ CONTROL: the chart read that words the message cannot make a voucher unpostable", async () => {
    // The disclosure needs a picture of the chart taken BEFORE the builder runs.
    // That read exists only to word an error message, so a failure of it must cost
    // the SENTENCE and never the VOUCHER — an operator who cannot post because a
    // diagnostic read failed is worse off than one who gets a vaguer error.
    const base = createMockClient({ seed: false });
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");
    await keyASaleableInvoice(app, base);

    // ⛔ NOT `mockImplementationOnce`, AND THAT IS NOT A STYLE CHOICE. The party
    // pick list and the screen's own chart both read `listLedgers` AT MOUNT, so a
    // queued one-shot rejection is consumed long before Ctrl+A and the arm passes
    // having tested nothing (§5.6: the probe that passes first try is the one to
    // distrust — this one did). The flag makes the ACCEPT's read the one that
    // fails, and `failedReads` proves it actually failed.
    const realList = spies.listLedgers!.getMockImplementation()!;
    let failNext = false;
    let failedReads = 0;
    spies.listLedgers!.mockImplementation((...args: unknown[]) => {
      if (failNext) {
        failNext = false;
        failedReads += 1;
        return Promise.reject(new Error("ledgers read failed"));
      }
      return realList(...args) as Promise<unknown>;
    });

    failNext = true;
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toMatch(/Saved/),
    );
    // The read really was the accept's, and it really did fail.
    expect(failedReads).toBe(1);
    // ⛔ AND THE VOUCHER IS ON THE BOOKS — the diagnostic read cost the sentence,
    // not the entry.
    expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1);
  });
});
