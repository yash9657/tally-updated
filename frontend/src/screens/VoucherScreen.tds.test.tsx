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
 * ⛔ REGISTER ROW D3 — A TDS DEDUCTION COULD NOT BE KEYED FROM THE KEYBOARD
 * ANYWHERE IN THIS PRODUCT.
 * ===========================================================================
 *
 * The Go wire has carried `tds` and `tcs` since T6 was built, the ledger derives
 * and PERSISTS the deduction, the TDS/TCS Register reads it and the whole challan
 * mechanism depends on it — and no form filled it. So on any books a real
 * operator keyed there were NO deductions at all, the challan's allocation
 * picker was empty, and D1, D2 and D4 were untestable against real data.
 *
 * ⚠️ EVERY TEST HERE DRIVES THE REAL SHELL AND PRESSES REAL KEYS, for the reason
 * the double-entry suite states: the feature's entire failure mode WAS being
 * unreachable, and a test that called `postJournalVoucher` directly would have
 * passed against the broken build.
 */

function spyClient(): {
  client: CoredClient;
  posted: () => VoucherRequest | undefined;
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
      for (const name of ["postJournalVoucher", "postPaymentVoucher"]) {
        const call = spies[name]?.mock.calls[0];
        if (call) return call[1] as VoucherRequest;
      }
      return undefined;
    },
  };
}

/**
 * ⛔ THE DEDUCTEE MUST EXIST. The panel resolves the ledger id from the chart and
 * REFUSES a name it cannot find, rather than creating a party to satisfy a 422 —
 * so a test that types a name into an empty mock is testing that refusal, not the
 * happy path. (That is how these tests first failed, and it is the right
 * behaviour: see the note on the party field.)
 */
async function seedDeductee(client: CoredClient) {
  await client.createLedger("co-1", {
    name: "Chitra Consulting",
    group_name: "Sundry Creditors",
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

function fillGrid(rows: [string, "DR" | "CR", string][]): void {
  for (let i = 0; i < rows.length; i++) {
    if (i > 0) fireEvent.click(screen.getByTestId("de-add-line"));
    fireEvent.change(screen.getByTestId(`de-ledger:${i}`), { target: { value: rows[i]![0] } });
    fireEvent.change(screen.getByTestId(`de-side:${i}`), { target: { value: rows[i]![1] } });
    fireEvent.change(screen.getByTestId(`de-amount:${i}`), { target: { value: rows[i]![2] } });
  }
}

/** The withholding entry every test below keys: 200000 booked, 180000 paid, 20000 held. */
function keyTheWithholdingEntry() {
  fillGrid([
    ["Professional Fees", "DR", "200000"],
    ["HDFC Bank", "CR", "180000"],
    ["TDS Payable", "CR", "20000"],
  ]);
}

async function openTaxPanel() {
  fireEvent.click(await screen.findByTestId("tax-tds-open"));
  return await screen.findByTestId("tax-tds-panel");
}

async function keyTheDeduction(over: { section?: string; leg?: string; amount?: string } = {}) {
  await waitFor(() =>
    expect(
      screen.getByTestId("tax-tds-section").querySelectorAll("option").length,
    ).toBeGreaterThan(1),
  );
  fireEvent.change(screen.getByTestId("tax-tds-section"), {
    target: { value: over.section ?? "194J-P" },
  });
  fireEvent.change(screen.getByTestId("tax-tds-party"), {
    target: { value: "Chitra Consulting" },
  });
  fireEvent.change(screen.getByTestId("tax-tds-amount"), {
    target: { value: over.amount ?? "200000" },
  });
  if (over.leg !== "") {
    fireEvent.change(screen.getByTestId("tax-tds-leg"), {
      target: { value: over.leg ?? "TDS Payable" },
    });
  }
}

describe("⛔ D3: the block is REACHABLE — the gap was that it was not", () => {
  it("F7 offers a TDS block, and it is COLLAPSED until asked for", async () => {
    renderShell(spyClient().client);
    fireEvent.keyDown(screen.getByRole("application"), { key: "F7" });
    // Most vouchers deduct nothing; a block that is always open invites a
    // half-keyed one on every entry.
    expect(await screen.findByTestId("tax-tds-open")).toBeTruthy();
    expect(screen.queryByTestId("tax-tds-panel")).toBeNull();
    fireEvent.click(screen.getByTestId("tax-tds-open"));
    expect(await screen.findByTestId("tax-tds-panel")).toBeTruthy();
  });

  it("⛔ it is on the DOUBLE-ENTRY form and NOT on single entry — an accounting fact", async () => {
    // Withholding splits the payment three ways (Dr expense in full, Cr bank NET,
    // Cr liability). Single entry puts the SUM of the particulars on ONE account,
    // so the withheld portion has nowhere to go but a negative particular.
    renderShell(spyClient().client);
    fireEvent.keyDown(screen.getByRole("application"), { key: "F5" });
    await screen.findByTestId("single-entry-panel");
    expect(screen.queryByTestId("tax-tds-open")).toBeNull();
  });

  it("a PAYMENT switched to double entry (Ctrl+H) gets it too", async () => {
    renderShell(spyClient().client);
    fireEvent.keyDown(screen.getByRole("application"), { key: "F5" });
    await screen.findByTestId("single-entry-panel");
    fireEvent.keyDown(screen.getByRole("application"), { key: "h", ctrlKey: true });
    fireEvent.mouseDown(await screen.findByText("Double Entry"));
    await screen.findByTestId("double-entry-panel");
    expect(await screen.findByTestId("tax-tds-open")).toBeTruthy();
  });

  it("⛔ the section list is SERVED, never hard-coded — and nothing is pre-selected", async () => {
    renderShell(spyClient().client);
    fireEvent.keyDown(screen.getByRole("application"), { key: "F7" });
    await openTaxPanel();
    const sel = screen.getByTestId("tax-tds-section") as HTMLSelectElement;
    await waitFor(() => expect(sel.querySelectorAll("option").length).toBeGreaterThan(1));
    const options = [...sel.querySelectorAll("option")].map((o) => o.getAttribute("value"));
    expect(options).toContain("194J-P");
    expect(options).toContain("194C");
    // ⛔ NO DEFAULT. The section decides the rate AND the code the return files
    // the payment under; picking one for the operator states what kind of
    // payment it is.
    expect(sel.value).toBe("");
  });
});

describe("⛔ D3: what reaches the wire", () => {
  it("the voucher carries the tds block, with the leg the server can verify", async () => {
    const { client, posted } = spyClient();
    await seedDeductee(client);
    renderShell(client);
    fireEvent.keyDown(screen.getByRole("application"), { key: "F7" });
    await screen.findByTestId("double-entry-panel");
    keyTheWithholdingEntry();
    await openTaxPanel();
    await keyTheDeduction();
    fireEvent.keyDown(screen.getByRole("application"), { key: "a", ctrlKey: true });

    await waitFor(() => expect(posted()).toBeDefined());
    const req = posted()!;
    expect(req.tds).toBeDefined();
    expect(req.tds!.section).toBe("194J-P");
    expect(req.tds!.taxable_amount).toBe("200000.0000");
    expect(req.tds!.pan_available).toBe(true);

    // ⛔ THE LEG ID IS A POSTED LEG. The server compares tds_ledger_id against
    // the postings; an id that matches none makes the verification sum zero and
    // compare it against the derived tax — a mismatch nobody can explain, or a
    // silent pass that verified nothing.
    const legID = req.tds!.tds_ledger_id;
    expect(legID).toBeTruthy();
    const leg = req.postings?.find((p) => p.ledger_id === legID);
    expect(leg).toBeDefined();
    expect(leg!.dr_cr.toUpperCase().startsWith("C")).toBe(true);
    expect(leg!.amount).toBe("20000.0000");
  });

  it("a voucher with NO deduction keyed carries no tds block at all", async () => {
    const { client, posted } = spyClient();
    await seedDeductee(client);
    renderShell(client);
    fireEvent.keyDown(screen.getByRole("application"), { key: "F7" });
    await screen.findByTestId("double-entry-panel");
    fillGrid([
      ["Professional Fees", "DR", "200000"],
      ["HDFC Bank", "CR", "200000"],
    ]);
    fireEvent.keyDown(screen.getByRole("application"), { key: "a", ctrlKey: true });
    await waitFor(() => expect(posted()).toBeDefined());
    expect(posted()!.tds).toBeUndefined();
  });
});

describe("⛔ D3: a half-keyed block is refused BEFORE any ledger is created", () => {
  it("no section — and NOTHING is posted and NO ledger is left behind", async () => {
    // D39's lesson: `ensureLedger` CREATES, so a refusal after resolution leaves
    // one new account per named row behind on every rejected Ctrl+A.
    const { client, posted } = spyClient();
    await seedDeductee(client);
    renderShell(client);
    fireEvent.keyDown(screen.getByRole("application"), { key: "F7" });
    await screen.findByTestId("double-entry-panel");
    keyTheWithholdingEntry();
    await openTaxPanel();
    await waitFor(() =>
      expect(
        screen.getByTestId("tax-tds-section").querySelectorAll("option").length,
      ).toBeGreaterThan(1),
    );
    fireEvent.change(screen.getByTestId("tax-tds-party"), {
      target: { value: "Chitra Consulting" },
    });
    fireEvent.change(screen.getByTestId("tax-tds-amount"), { target: { value: "200000" } });

    fireEvent.keyDown(screen.getByRole("application"), { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(screen.getByTestId("tax-tds-refusal").textContent).toContain("NO DEFAULT"),
    );
    expect(posted()).toBeUndefined();
    const { ledgers } = await client.listLedgers("co-1");
    expect(ledgers.map((l) => l.name)).not.toContain("Professional Fees");
  });

  it("⛔ a named tax leg that is not on the grid is refused, naming what is missing", async () => {
    const { client, posted } = spyClient();
    await seedDeductee(client);
    renderShell(client);
    fireEvent.keyDown(screen.getByRole("application"), { key: "F7" });
    await screen.findByTestId("double-entry-panel");
    // NO "TDS Payable" line: the money is shown as fully paid.
    fillGrid([
      ["Professional Fees", "DR", "200000"],
      ["HDFC Bank", "CR", "200000"],
    ]);
    await openTaxPanel();
    await keyTheDeduction({ leg: "" });
    // The picker only offers grid rows, so name it the way a stale form would:
    // by keying the grid and then removing the line. Simulated by choosing a row
    // that is present and then editing it away.
    fireEvent.change(screen.getByTestId("tax-tds-leg"), {
      target: { value: "Professional Fees" },
    });
    fireEvent.change(screen.getByTestId("de-ledger:0"), { target: { value: "Consulting Fees" } });
    fireEvent.keyDown(screen.getByRole("application"), { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(screen.getByTestId("tax-tds-refusal").textContent).toContain(
        "no line on this voucher names that ledger",
      ),
    );
    expect(posted()).toBeUndefined();
  });
});

describe("⛔ D3: the figures shown are the SERVER'S, and the gaps are disclosed", () => {
  it("the rate and threshold under the picker come from the served catalog", async () => {
    renderShell(spyClient().client);
    fireEvent.keyDown(screen.getByRole("application"), { key: "F7" });
    await openTaxPanel();
    await waitFor(() =>
      expect(
        screen.getByTestId("tax-tds-section").querySelectorAll("option").length,
      ).toBeGreaterThan(1),
    );
    fireEvent.change(screen.getByTestId("tax-tds-section"), { target: { value: "194C" } });
    const rate = await screen.findByTestId("tax-tds-rate");
    expect(rate.textContent).toContain("2%"); // OTHER; 194C is the only split
    expect(rate.textContent).toContain("30000.0000");
    expect(rate.textContent).toContain("The server derives the tax");
  });

  it("unticking PAN says the rate RISES, rather than moving it silently", async () => {
    renderShell(spyClient().client);
    fireEvent.keyDown(screen.getByRole("application"), { key: "F7" });
    await openTaxPanel();
    await waitFor(() =>
      expect(
        screen.getByTestId("tax-tds-section").querySelectorAll("option").length,
      ).toBeGreaterThan(1),
    );
    fireEvent.change(screen.getByTestId("tax-tds-section"), { target: { value: "194C" } });
    fireEvent.click(screen.getByTestId("tax-tds-pan"));
    expect((await screen.findByTestId("tax-tds-rate")).textContent).toContain("HIGHER of");
  });

  it("⛔ a deduction recorded with NO leg is DISCLOSED, not silently accepted", async () => {
    const { client, posted } = spyClient();
    await seedDeductee(client);
    renderShell(client);
    fireEvent.keyDown(screen.getByRole("application"), { key: "F7" });
    await screen.findByTestId("double-entry-panel");
    fillGrid([
      ["Professional Fees", "DR", "200000"],
      ["HDFC Bank", "CR", "200000"],
    ]);
    await openTaxPanel();
    await keyTheDeduction({ leg: "" });
    // Legal and real (some companies book withholdings by a separate journal),
    // so it is said out loud rather than refused: the books show the full amount
    // paid out while the return says a fifth was withheld.
    expect((await screen.findByTestId("tax-tds-unposted")).textContent).toContain(
      "full amount as paid out",
    );
    fireEvent.keyDown(screen.getByRole("application"), { key: "a", ctrlKey: true });
    await waitFor(() => expect(posted()).toBeDefined());
    expect(posted()!.tds).toBeDefined();
    expect(posted()!.tds).not.toHaveProperty("tds_ledger_id");
  });

  it("hiding the panel while it is keyed still says the voucher carries a deduction", async () => {
    const c = spyClient().client;
    await seedDeductee(c);
    renderShell(c);
    fireEvent.keyDown(screen.getByRole("application"), { key: "F7" });
    await screen.findByTestId("double-entry-panel");
    keyTheWithholdingEntry();
    await openTaxPanel();
    await keyTheDeduction();
    fireEvent.click(screen.getByTestId("tax-tds-close"));
    expect((await screen.findByTestId("tax-tds-collapsed")).textContent).toContain("194J-P");
  });
});

/**
 * ===========================================================================
 * ⛔⛔ REGISTER ROW D88 — ALTERING A VOUCHER SILENTLY DELETED ITS DEDUCTION.
 * ===========================================================================
 * `deleteTDSDeductions` runs on EVERY alter and the row is re-derived from the
 * body — correct on its own terms: a voucher altered to carry no TDS at all must
 * lose its deduction rather than keep a stale one. What was missing was the READ
 * path, so the form could not restate what it had loaded.
 *
 * ⛔ AND THE LOSS IS INVISIBLE ON THE BOOKS. The withheld credit is a posting and
 * the postings are restated, so the entry is byte-identical afterwards. Only the
 * return changed — and the challan that paid the deduction kept an allocation
 * whose natural key now matches no row (migration 0069 carries no FK by design).
 * That is why this test asserts the BLOCK on the wire and not the postings.
 */
describe("⛔ D88: an alter RE-STATES the deduction instead of deleting it", () => {
  it("the block comes back on the form and goes back up on the next Ctrl+A", async () => {
    const base = createMockClient({ seed: false });
    await seedDeductee(base);
    const spies: Record<string, ReturnType<typeof vi.fn>> = {};
    const client = {} as Record<string, unknown>;
    for (const key of Object.keys(base) as (keyof CoredClient)[]) {
      const orig = base[key] as unknown as (...a: unknown[]) => unknown;
      spies[key] = vi.fn((...args: unknown[]) => orig.apply(base, args));
      client[key] = spies[key];
    }
    render(
      <CompanyProvider
        client={client as unknown as CoredClient}
        initialCompany={{ id: "co-1", name: "Test Co", homeStateCode: "29" }}
      >
        <AppShell />
      </CompanyProvider>,
    );
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F7" });
    await screen.findByTestId("double-entry-panel");
    keyTheWithholdingEntry();
    await openTaxPanel();
    await keyTheDeduction();
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.postJournalVoucher).toHaveBeenCalledTimes(1));
    const posted = await spies.postJournalVoucher!.mock.results[0]!.value;

    // Drill in from the Day Book, the way an operator opens a voucher to fix it.
    fireEvent.keyDown(app, { key: "Escape" });
    fireEvent.keyDown(app, { key: "g", altKey: true });
    fireEvent.mouseDown(screen.getByText("Day Book"));
    fireEvent.keyDown(app, { key: "Enter" });
    await waitFor(() => expect(screen.getByText(posted.voucher_no)).toBeTruthy());
    fireEvent.keyDown(app, { key: "Enter" });
    await waitFor(() => expect(spies.getVoucher).toHaveBeenCalled());

    // ⛔ VISIBLE ON THE FORM. The collapsed block SAYS the voucher carries a
    // deduction — an alter that will post one while nothing on screen mentions
    // it is the undisclosed-figure defect, and this is the surface that stops it.
    await waitFor(() =>
      expect(screen.getByTestId("tax-tds-collapsed").textContent).toMatch(
        /194J-P/,
      ),
    );
    // ⛔ AND IT NAMES THE DEDUCTEE, NOT "(nobody)". `taxDeductionFromView` is
    // pure and runs before the chart has loaded, so it resolves the party from
    // the VOUCHER'S OWN POSTINGS — and a deductee is very often not a leg at all
    // (a rent withholding debits Rent and credits the bank; the landlord appears
    // nowhere). The panel repairs the name once the chart arrives. Seen reading
    // "on (nobody)" over a real party in a browser on 2026-08-21.
    await waitFor(() =>
      expect(screen.getByTestId("tax-tds-collapsed").textContent).toMatch(
        /Chitra Consulting/,
      ),
    );
    expect(screen.getByTestId("tax-tds-collapsed").textContent).not.toMatch(
      /nobody/,
    );

    // The ordinary round trip: change nothing, save.
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() => expect(spies.alterJournalVoucher).toHaveBeenCalledTimes(1));
    const req = spies.alterJournalVoucher!.mock.calls[0]![2] as VoucherRequest;
    expect(req.tds).toBeDefined();
    expect(req.tds!.section).toBe("194J-P");
    expect(req.tds!.taxable_amount).toBe("200000.0000");
    expect(req.tds!.deductee_ledger_id).toBeTruthy();
  });
});
