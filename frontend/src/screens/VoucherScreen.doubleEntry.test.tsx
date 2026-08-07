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
import type {
  CoredClient,
  CostAllocationInput,
  PostingRequest,
} from "../api";
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
 * THE DOUBLE-ENTRY POSTING GRID — F7, AND Ctrl+H > DOUBLE ENTRY ON F5/F6/F4
 * ===========================================================================
 *
 * ⛔ WHAT THIS CLOSES: **a Journal could not be keyed at all**, on a
 * FeaturesScreen row that read `on`. Measured by driving the shell on
 * 2026-08-06: F7 rendered EXACTLY ONE INPUT, "Dr/Cr Ledger" — no amount field,
 * no second line, no Dr/Cr selector. `buildPlainRequest` forwarded
 * `f.postings`, which is only ever populated by hydrating a LOADED voucher, so
 * a fresh Journal posted an empty body or nothing at all.
 *
 * That is HANDOFF §5.1's HIDDEN kind of parity gap — worse than an honest
 * `off`, because an evaluator hits it in week one and concludes the product is
 * broken rather than incomplete. A Journal is how every adjustment, provision,
 * depreciation and reclass is keyed, so it blocked every business at every
 * period end.
 *
 * ⚠️ EVERY TEST HERE DRIVES THE REAL SHELL AND PRESSES REAL KEYS. The feature's
 * entire failure mode was being unreachable, and a test that calls
 * `postJournalVoucher` directly would have passed against the broken build.
 * That is the mistake HANDOFF §1.5 records making and catching by mutation.
 */

function spyClient(base: CoredClient = createMockClient({ seed: false })): {
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

/** Key N Dr/Cr lines into the grid. */
function fillGrid(rows: [string, "DR" | "CR", string][]): void {
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
}

/**
 * Resolve posted ledger_ids back to NAMES.
 *
 * ⚠️ Counting sides is not enough to pin a grid. A two-line entry has exactly
 * one debit and one credit whichever way round it is built, so a test asserting
 * only lengths and amounts passes just as happily on a REVERSED voucher — and
 * on this form the operator states the side explicitly, so getting it backwards
 * would mean the Dr/Cr selector is ignored.
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
      .map((p): [string, string] => [
        nameOf.get(p.ledger_id) ?? p.ledger_id,
        p.amount,
      ])
      .sort((a, b) => a[0].localeCompare(b[0]));
  return { debits: on("D"), credits: on("C") };
}

describe("the grid is REACHABLE — the gap was that it was not", () => {
  it("F7 opens a Journal form that has a posting grid at all", async () => {
    renderShell(spyClient().client);
    fireEvent.keyDown(screen.getByRole("application"), { key: "F7" });

    // ⛔ THE ASSERTION THE OLD BUILD FAILED. The panel simply was not rendered:
    // the `double` branch fell through to `null`.
    expect(await screen.findByTestId("double-entry-panel")).toBeTruthy();
    // ...and it has the three controls whose ABSENCE was the measured defect.
    expect(screen.getByTestId("de-side:0")).toBeTruthy();
    expect(screen.getByTestId("de-ledger:0")).toBeTruthy();
    expect(screen.getByTestId("de-amount:0")).toBeTruthy();
    // Not the single-entry form: a Journal states both sides and infers none.
    expect(screen.queryByTestId("single-entry-panel")).toBeNull();
  });

  /**
   * ⛔ THE HOLE WAS FOUR CLASSES, NOT ONE, AND HANDOFF §4.5 NAMED ONLY THE
   * JOURNAL. `effectiveEntryMode` honours a Ctrl+H choice of "double" on the
   * three single-entry classes, and that branch rendered `null` exactly as F7
   * did — so Payment, Receipt and Contra reached the identical dead end one
   * keystroke away. The Journal was merely the class that DEFAULTS there.
   *
   * This is HANDOFF §9.1a item 7 again: when a screen is parameterised by type,
   * drive more than one type.
   */
  for (const [fkey, cls] of [
    ["F5", "Payment"],
    ["F6", "Receipt"],
    ["F4", "Contra"],
  ] as const) {
    it(`${fkey} (${cls}) + Ctrl+H > Double Entry also reaches the grid`, async () => {
      renderShell(spyClient().client);
      const app = screen.getByRole("application");
      fireEvent.keyDown(app, { key: fkey });
      // The control: it starts in single entry, so the grid appearing is
      // caused by the keystroke rather than being there all along.
      expect(await screen.findByTestId("single-entry-panel")).toBeTruthy();
      expect(screen.queryByTestId("double-entry-panel")).toBeNull();

      fireEvent.keyDown(app, { key: "h", ctrlKey: true });
      fireEvent.mouseDown(await screen.findByText("Double Entry"));

      expect(await screen.findByTestId("double-entry-panel")).toBeTruthy();
      expect(screen.queryByTestId("single-entry-panel")).toBeNull();
    });
  }

  it("Ctrl+H back to Single Entry does not LOSE what was keyed in the grid", async () => {
    // The two modes keep separate line arrays precisely so this is lossless.
    // A shared array would mean the switch silently reinterprets rows that mean
    // "the many side of an inferred entry" in one mode and "the whole entry" in
    // the other.
    renderShell(spyClient().client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F5" });
    fireEvent.keyDown(app, { key: "h", ctrlKey: true });
    fireEvent.mouseDown(await screen.findByText("Double Entry"));
    await screen.findByTestId("double-entry-panel");
    fillGrid([["Depreciation", "DR", "1250"]]);

    fireEvent.keyDown(app, { key: "h", ctrlKey: true });
    fireEvent.mouseDown(await screen.findByText(/Single Entry/));
    await screen.findByTestId("single-entry-panel");

    fireEvent.keyDown(app, { key: "h", ctrlKey: true });
    fireEvent.mouseDown(await screen.findByText("Double Entry"));
    await screen.findByTestId("double-entry-panel");
    expect(
      (screen.getByTestId("de-ledger:0") as HTMLInputElement).value,
    ).toBe("Depreciation");
    expect(
      (screen.getByTestId("de-amount:0") as HTMLInputElement).value,
    ).toBe("1250");
  });
});

describe("a keyed Journal posts the postings the operator STATED", () => {
  it("posts both sides, named by ledger, in the sides the grid says", async () => {
    const { client, spies } = spyClient();
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F7" });
    await screen.findByTestId("double-entry-panel");

    // A depreciation entry — the archetypal period-end Journal.
    fillGrid([
      ["Depreciation", "DR", "1250"],
      ["Accumulated Depreciation", "CR", "1250"],
    ]);
    expect(screen.getByTestId("de-difference").textContent).toContain(
      "balanced",
    );

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(spies.postJournalVoucher).toHaveBeenCalledTimes(1),
    );

    const [, req] = spies.postJournalVoucher!.mock.calls[0]!;
    const postings = (req as { postings: PostingRequest[] }).postings;
    expect(postings).toHaveLength(2);

    // ⛔ NAMED BY LEDGER ON EACH SIDE. A builder that ignored the Dr/Cr selector
    // and oriented by class would put both of these the same way round and
    // still balance.
    const { debits, credits } = await sidesByName(client, postings);
    expect(debits).toEqual([["Depreciation", "1250.0000"]]);
    expect(credits).toEqual([["Accumulated Depreciation", "1250.0000"]]);
  });

  it("posts a MANY-line reclass with an uneven split on each side", async () => {
    // The control for the two-line case above, which cannot distinguish
    // "honours each row's side" from "alternates Dr, Cr, Dr, Cr".
    const { client, spies } = spyClient();
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F7" });
    await screen.findByTestId("double-entry-panel");

    fillGrid([
      ["Salaries", "DR", "700"],
      ["Wages", "DR", "300"],
      ["Provision for Bonus", "CR", "1000"],
    ]);
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(spies.postJournalVoucher).toHaveBeenCalledTimes(1),
    );

    const [, req] = spies.postJournalVoucher!.mock.calls[0]!;
    const { debits, credits } = await sidesByName(
      client,
      (req as { postings: PostingRequest[] }).postings,
    );
    expect(debits).toEqual([
      ["Salaries", "700.0000"],
      ["Wages", "300.0000"],
    ]);
    expect(credits).toEqual([["Provision for Bonus", "1000.0000"]]);
  });

  it("ignores the blank trailing row the grid always offers", async () => {
    const { client, spies } = spyClient();
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F7" });
    await screen.findByTestId("double-entry-panel");

    fillGrid([
      ["Depreciation", "DR", "1250"],
      ["Accumulated Depreciation", "CR", "1250"],
    ]);
    fireEvent.click(screen.getByTestId("de-add-line")); // a third, blank row
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(spies.postJournalVoucher).toHaveBeenCalledTimes(1),
    );
    const [, req] = spies.postJournalVoucher!.mock.calls[0]!;
    expect((req as { postings: PostingRequest[] }).postings).toHaveLength(2);
  });
});

describe("⛔ THE CONTROL: an unbalanced grid is refused BY THE FORM", () => {
  it("shows the difference live, before Ctrl+A is ever pressed", async () => {
    renderShell(spyClient().client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F7" });
    await screen.findByTestId("double-entry-panel");

    fillGrid([
      ["Rent", "DR", "5000"],
      ["HDFC Bank", "CR", "3000"],
    ]);
    expect(screen.getByTestId("de-difference").textContent).toContain(
      "2000.0000",
    );
    const refusal = screen.getByTestId("de-refusal").textContent ?? "";
    expect(refusal).toContain("5000.0000");
    expect(refusal).toContain("3000.0000");
    expect(refusal).toContain("2000.0000");
  });

  it("makes NO request at all — the 422 never has to happen", async () => {
    const { client, spies } = spyClient();
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F7" });
    await screen.findByTestId("double-entry-panel");

    fillGrid([
      ["Rent", "DR", "5000"],
      ["HDFC Bank", "CR", "3000"],
    ]);
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    // The status line carries the refusal, so the operator is told — a silent
    // no-op would read as "Ctrl+A is broken".
    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toContain(
        "Debits exceed credits",
      ),
    );
    expect(spies.postJournalVoucher).not.toHaveBeenCalled();
  });

  /**
   * ⛔⛔ AND IT CREATES NO LEDGER. This is why `buildDoubleEntryRequest`
   * validates BEFORE it resolves: `ensureLedger` CREATES a ledger that does not
   * exist yet, so a builder that validated after resolution would leave a real
   * account behind in the chart on every rejected Ctrl+A. Press it five times
   * on an entry that is 200 short and the company silently gains five ledgers
   * and no voucher, with nothing on screen saying so.
   *
   * Move the `doubleEntryRefusal` check below the resolve loop and this test
   * goes red while every other test in this file stays green.
   */
  it("creates NO ledger as a side effect of being refused", async () => {
    const { client, base, spies } = spyClient();
    const before = (await base.listLedgers("co-1")).ledgers.length;
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F7" });
    await screen.findByTestId("double-entry-panel");

    // Two ledgers that certainly do not exist yet on an unseeded company.
    fillGrid([
      ["Brand New Expense", "DR", "5000"],
      ["Brand New Liability", "CR", "3000"],
    ]);
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toContain("exceed"),
    );
    expect(spies.createLedger).not.toHaveBeenCalled();
    expect((await base.listLedgers("co-1")).ledgers).toHaveLength(before);
  });

  it("refuses a one-sided voucher by naming the MISSING side", async () => {
    const { client, spies } = spyClient();
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F7" });
    await screen.findByTestId("double-entry-panel");

    fillGrid([
      ["Salaries", "DR", "700"],
      ["Wages", "DR", "300"],
    ]);
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toContain("CREDIT"),
    );
    expect(spies.postJournalVoucher).not.toHaveBeenCalled();
  });

  it("refuses an empty grid instead of posting a voucher with no postings", async () => {
    const { client, spies } = spyClient();
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F7" });
    await screen.findByTestId("double-entry-panel");

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toContain(
        "Nothing has been keyed",
      ),
    );
    expect(spies.postJournalVoucher).not.toHaveBeenCalled();
  });
});

describe("cost allocation on a Journal — posting_seq is 0-BASED with NO offset", () => {
  /**
   * ⛔ THE SECOND CALL SITE `buildCostAllocations`' `primaryLegCount` EXISTS
   * FOR. Single entry passes 1 because its postings array is [the account paid
   * from, ...the particulars]; this grid passes 0 because its rows map
   * one-for-one from index 0.
   *
   * The two differ by one and the wrong one is SILENT: the split attaches to a
   * real ledger with a plausible amount, on a voucher that balances perfectly
   * and a Cost Centre Summary that foots. Nothing downstream can detect it.
   *
   * ⚠️ The distinguishing input is a split on a row that is NOT row 0 — with
   * only row 0 allocated, offsets 0 and 1 both produce a seq the server
   * accepts, and the wrong one silently moves the cost to the other account.
   */
  it("allocates the row the operator keyed, not the one after it", async () => {
    const base = createMockClient({ seed: false });
    // ⚠️ THE MOCK'S OWN FIXED AXES, not ones created here. `mockCostCategories`
    // serves Department/Branch unconditionally, so creating either is a
    // duplicate-name refusal — which the mock makes because the server does.
    const axes = (await base.listCostCategories("co-1")).cost_categories;
    const cat = axes.find((a) => a.name === "Department")!;
    const centre = (await base.listCostCentres("co-1")).cost_centres.find(
      (c) => c.cost_category_id === cat.id,
    )!;

    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F7" });
    await screen.findByTestId("double-entry-panel");

    fillGrid([
      ["Provision for Bonus", "CR", "1000"],
      ["Salaries", "DR", "1000"],
    ]);

    // Allocate the SECOND row (index 1) — the one whose seq differs between a
    // correct offset of 0 and single entry's offset of 1.
    fireEvent.click(await screen.findByTestId("alloc:1-add"));
    fireEvent.change(screen.getByTestId("alloc:1-axis:0"), {
      target: { value: cat.id },
    });
    fireEvent.change(screen.getByTestId("alloc:1-centre:0"), {
      target: { value: centre.id },
    });
    fireEvent.change(screen.getByTestId("alloc:1-amount:0"), {
      target: { value: "1000" },
    });

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(spies.postJournalVoucher).toHaveBeenCalledTimes(1),
    );

    const [, req] = spies.postJournalVoucher!.mock.calls[0]!;
    const body = req as {
      postings: PostingRequest[];
      cost_allocations?: CostAllocationInput[];
    };
    expect(body.cost_allocations).toHaveLength(1);
    // ⛔ SEQ 1 — the index of "Salaries" in the postings array. With single
    // entry's offset of 1 this would be 2, which on a two-line voucher is a
    // posting that does not exist; on a longer grid it would be a real,
    // wrong ledger.
    expect(body.cost_allocations![0]!.posting_seq).toBe(1);
    // And that seq really is the row the operator allocated.
    const nameOf = new Map(
      (await client.listLedgers("co-1")).ledgers.map((l) => [l.id, l.name]),
    );
    expect(nameOf.get(body.postings[1]!.ledger_id)).toBe("Salaries");
  });
});

describe("⛔⛔ ALTERING a Journal keeps its postings — the read-back", () => {
  /**
   * `posting` is keyed on `voucher_version_id`, so an ALTER RE-STATES: the new
   * version's postings are EXACTLY what the body carries. The grid now owns
   * that path — `buildDoubleEntryRequest` builds `postings` from `drCrLines`
   * and reads nothing else — so a `formFromView` that failed to hydrate the
   * grid would make "open a Journal, fix the narration, save" post a version
   * with NO POSTINGS. The entry silently reverses out of every balance and
   * nothing refuses it, because zero postings is a legal state.
   *
   * This is HANDOFF §4.3's shape for the fifth time, and §12 item 4a's check.
   *
   * ⚠️ IT DRIVES THE FORM. Calling `alterJournalVoucher` directly would prove
   * the STORE round-trips and prove nothing about the screen — the mistake
   * §1.5 records making, catching by mutation, and fixing.
   */
  it("re-sends through the FORM what the GET returned", async () => {
    const { client, base, spies } = spyClient();
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F7" });
    await screen.findByTestId("double-entry-panel");
    fillGrid([
      ["Depreciation", "DR", "1250"],
      ["Accumulated Depreciation", "CR", "1250"],
    ]);
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(spies.postJournalVoucher).toHaveBeenCalledTimes(1),
    );
    const posted = await spies.postJournalVoucher!.mock.results[0]!.value;

    // Drill Day Book -> the row -> the ALTER frame, the way an operator does.
    fireEvent.keyDown(app, { key: "Escape" });
    fireEvent.keyDown(app, { key: "g", altKey: true });
    fireEvent.mouseDown(screen.getByText("Day Book"));
    fireEvent.keyDown(app, { key: "Enter" });
    await waitFor(() =>
      expect(screen.getByText(posted.voucher_no)).toBeTruthy(),
    );
    fireEvent.keyDown(app, { key: "Enter" });
    await waitFor(() =>
      expect(screen.getByText(/Journal Voucher \(Alteration\)/)).toBeTruthy(),
    );
    await waitFor(() => expect(spies.getVoucher).toHaveBeenCalled());

    // ⛔ THE HYDRATION IS VISIBLE: both sides came back into the grid.
    await screen.findByTestId("double-entry-panel");
    await waitFor(() =>
      expect(
        (screen.getByTestId("de-ledger:0") as HTMLInputElement).value,
      ).not.toBe(""),
    );
    expect(screen.getByTestId("de-difference").textContent).toContain(
      "balanced",
    );

    // Edit only the narration and save.
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(spies.alterJournalVoucher).toHaveBeenCalledTimes(1),
    );

    const [, , req] = spies.alterJournalVoucher!.mock.calls[0]!;
    const sent = (req as { postings: PostingRequest[] }).postings;
    expect(sent).toHaveLength(2);
    const { debits, credits } = await sidesByName(client, sent);
    expect(debits).toEqual([["Depreciation", "1250.0000"]]);
    expect(credits).toEqual([["Accumulated Depreciation", "1250.0000"]]);

    // ...and the store still holds them afterwards.
    const kept = await base.getVoucher("co-1", posted.voucher_id);
    expect(kept.postings).toHaveLength(2);
  });

  /**
   * ⛔ THE INVERSE CONTROL, and without it the test above proves nothing.
   *
   * "Altering keeps the postings" is equally consistent with postings being
   * IMMUNE to an alter — in which case the hydration is decoration. This
   * establishes that the behaviour the test above depends on is genuinely
   * dangerous: a body that omits them really does destroy the entry.
   */
  it("⛔ CONTROL: an alter that OMITS the postings destroys the entry", async () => {
    const base = createMockClient({ seed: false });
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F7" });
    await screen.findByTestId("double-entry-panel");
    fillGrid([
      ["Depreciation", "DR", "1250"],
      ["Accumulated Depreciation", "CR", "1250"],
    ]);
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(spies.postJournalVoucher).toHaveBeenCalledTimes(1),
    );
    const posted = await spies.postJournalVoucher!.mock.results[0]!.value;
    expect((await base.getVoucher("co-1", posted.voucher_id)).postings).toHaveLength(2);

    await base.alterJournalVoucher("co-1", posted.voucher_id, {
      voucher_date: "2026-08-06",
      narration: "typo fixed",
      party: "",
      postings: [],
    });

    const wiped = await base.getVoucher("co-1", posted.voucher_id);
    expect(wiped.postings).toHaveLength(0);
  });
});

describe("⛔⛔ a fresh voucher does not inherit the LAST one's party", () => {
  /**
   * ===========================================================================
   * A LIVE, PRE-EXISTING, MONEY-CORRUPTING DEFECT — FOUND BY THIS TRACK, AND
   * NOT PART OF IT.
   * ===========================================================================
   *
   * `AccountingVoucherScreen` is not remounted when the frame's voucher type
   * changes (F8 and F6 render the same component instance) and NOTHING reset
   * `form`. So `party` and `partyLedgerId` survived from one voucher into the
   * next, and `buildSingleEntryRequest` uses `f.party` as the account the money
   * moved through.
   *
   * Driven on the shell, this posted:
   *
   *     DR Acme Traders 2500 / CR Electricity 2500
   *
   * for a RECEIPT whose operator had named no account at all — money debited to
   * the previous voucher's CUSTOMER instead of a bank. Nothing can refuse it:
   * the voucher balances, and debiting a debtor is perfectly legal.
   *
   * ⛔ AND THE OPERATOR COULD NOT SEE IT. `SelectionList` was uncontrolled, so
   * the Account field rendered EMPTY over the stale value — two invisibilities
   * compounding, which is why it survived. It surfaced only because adding the
   * `value` prop turned three unrelated tests red.
   *
   * ⚠️ THE CONTROL IS THE SECOND TEST: the reset must NOT fire when a document
   * is being hydrated, or a drill would blank the voucher the operator just
   * opened — trading a silent wrong posting for silent data loss.
   */
  it("F8 then F6: the receipt does NOT post against the sale's customer", async () => {
    const base = createMockClient({ seed: false });
    await base.createLedger("co-1", {
      name: "Acme Traders",
      group_name: "Sundry Debtors",
    });
    const { client, spies } = spyClient(base);
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F8" });
    fireEvent.mouseDown(await screen.findByText("Acme Traders"));
    fireEvent.change(screen.getByTestId("gst-taxable"), {
      target: { value: "10000" },
    });
    fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1),
    );

    // A fresh Receipt. The operator names NO account and fills only the line.
    fireEvent.keyDown(app, { key: "F6" });
    await screen.findByTestId("single-entry-panel");

    // ⛔ THE FIELD IS CLEAR. Before the fix it held "Acme Traders" — and before
    // the `value` prop it held "Acme Traders" INVISIBLY, which is worse.
    expect(
      (screen.getByRole("combobox", { name: "Account" }) as HTMLInputElement)
        .value,
    ).toBe("");

    fireEvent.change(screen.getByTestId("single-ledger:0"), {
      target: { value: "Electricity" },
    });
    fireEvent.change(screen.getByTestId("single-amount:0"), {
      target: { value: "2500" },
    });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    // The voucher is refused for a NAMED, honest reason rather than silently
    // posting against a party nobody chose.
    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toContain("required"),
    );
    expect(spies.postReceiptVoucher).not.toHaveBeenCalled();
  });

  /**
   * A CROSS-CLASS DRILL STILL HYDRATES — the reset must not eat a document.
   *
   * ⚠️ THIS IS NOT A CONTROL FOR THE HYDRATION GUARD, AND ITS FIRST TWO DRAFTS
   * BOTH CLAIMED TO BE ONE. Draft 1 drilled a Journal from a Journal frame, so
   * `type` never changed and the reset effect never ran at all. Draft 2 fixed
   * that by standing on a Payment frame first — and deleting
   * `if (loadedVoucherID || isDraftMode) return;` STILL left the suite green,
   * because the reset runs first and the async `getVoucher` re-seeds the form
   * afterwards.
   *
   * So the guard is defensive and unproven, and the comment at its definition
   * says so rather than letting this test be mistaken for evidence (HANDOFF
   * §9.2: a test that cannot fail is worse than no test — and mislabelling one
   * as a control is how it gets trusted).
   *
   * What this test DOES prove is worth keeping: a drill that re-classes the
   * frame still ends up with the document's own lines in the grid.
   */
  it("a CROSS-CLASS drill still hydrates the grid", async () => {
    const { client, spies } = spyClient();
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F7" });
    await screen.findByTestId("double-entry-panel");
    fillGrid([
      ["Depreciation", "DR", "1250"],
      ["Accumulated Depreciation", "CR", "1250"],
    ]);
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(spies.postJournalVoucher).toHaveBeenCalledTimes(1),
    );
    const posted = await spies.postJournalVoucher!.mock.results[0]!.value;

    // ⛔ STAND ON A **PAYMENT** FRAME FIRST, so the drill has a class to cross.
    fireEvent.keyDown(app, { key: "Escape" });
    fireEvent.keyDown(app, { key: "F5" });
    await screen.findByTestId("single-entry-panel");

    fireEvent.keyDown(app, { key: "Escape" });
    fireEvent.keyDown(app, { key: "g", altKey: true });
    fireEvent.mouseDown(screen.getByText("Day Book"));
    fireEvent.keyDown(app, { key: "Enter" });
    await waitFor(() =>
      expect(screen.getByText(posted.voucher_no)).toBeTruthy(),
    );
    fireEvent.keyDown(app, { key: "Enter" });
    await waitFor(() => expect(spies.getVoucher).toHaveBeenCalled());
    // The frame really did re-class — otherwise this proves nothing.
    await waitFor(() =>
      expect(screen.getByText(/Journal Voucher \(Alteration\)/)).toBeTruthy(),
    );

    // The hydration survived the class change that the drill performed.
    await waitFor(() =>
      expect(
        (screen.getByTestId("de-ledger:0") as HTMLInputElement).value,
      ).toBe("Depreciation"),
    );
    expect(screen.getByTestId("de-difference").textContent).toContain(
      "balanced",
    );
  });
});

describe("the party field is no longer BLANK on an alteration", () => {
  /**
   * ⚠️ PRE-EXISTING AND NOT THIS TRACK'S — it did it on Sales too. `SelectionList`
   * is an uncontrolled type-ahead and nothing typed into it on an alter, so a
   * drilled voucher hydrated its postings, amounts, splits and bill references
   * and then showed an EMPTY Account field.
   *
   * It never corrupted anything: the posting is resolved by name from
   * `f.party` and was browser-proved correct. But an operator seeing a blank
   * Account on a voucher they just opened reads it as data loss — and the
   * natural response, retyping the party, is how a correct voucher gets edited
   * into a wrong one.
   */
  it("shows the loaded party when a voucher is drilled and hydrated", async () => {
    const { client, spies } = spyClient();
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F5" });
    await screen.findByTestId("single-entry-panel");
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
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(spies.postPaymentVoucher).toHaveBeenCalledTimes(1),
    );
    const posted = await spies.postPaymentVoucher!.mock.results[0]!.value;

    fireEvent.keyDown(app, { key: "Escape" });
    fireEvent.keyDown(app, { key: "g", altKey: true });
    fireEvent.mouseDown(screen.getByText("Day Book"));
    fireEvent.keyDown(app, { key: "Enter" });
    await waitFor(() =>
      expect(screen.getByText(posted.voucher_no)).toBeTruthy(),
    );
    fireEvent.keyDown(app, { key: "Enter" });
    await waitFor(() => expect(spies.getVoucher).toHaveBeenCalled());

    // ⛔ THE ASSERTION THAT WAS FAILING SILENTLY IN THE PRODUCT.
    await waitFor(() =>
      expect(
        (screen.getByRole("combobox", { name: "Account" }) as HTMLInputElement)
          .value,
      ).toBe("HDFC Bank"),
    );
  });

  it("does not clobber what the operator types on a FRESH voucher", async () => {
    // The control. A `value` prop that unconditionally won would make the field
    // untypable — the seed must yield to the operator the moment they type.
    renderShell(spyClient().client);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F5" });
    await screen.findByTestId("single-entry-panel");

    const box = screen.getByRole("combobox", {
      name: "Account",
    }) as HTMLInputElement;
    fireEvent.change(box, { target: { value: "Petty Cash" } });
    expect(box.value).toBe("Petty Cash");
    fireEvent.mouseDown(await screen.findByText(/Petty Cash/));
    // Selecting sets form.party, which flows back down as `value` — and must
    // NOT reset the field the operator is standing in.
    await waitFor(() => expect(box.value).toBe("Petty Cash"));
  });
});
