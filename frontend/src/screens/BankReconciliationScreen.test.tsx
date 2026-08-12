import { describe, it, expect, vi } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { CompanyProvider } from "../app/CompanyContext";
import {
  ScreenActionsProvider,
  useScreenActionDispatcher,
} from "../app/ScreenActions";
import { BankReconciliationScreen } from "./BankReconciliationScreen";
import { createMockClient } from "../api/mock";
import type { CoredClient } from "../api";
import type {
  BankReconLine,
  BankReconciliationResponse,
  LedgerListItem,
} from "../api";
import type { NavSnapshot } from "../keystroke/navMachine";
import { ApiError } from "../api";

/**
 * The BANK RECONCILIATION screen. Invariants I-7, I-9 and I-11 of
 * docs/design/t1t4-ui-model.md, plus the edge-case ledger in its §5.
 *
 * T4's server half shipped with no key anywhere in the client. What this suite
 * guards is not that the screen renders — it is that the screen does not undo the
 * invariants the server was built around: it must surface a broken
 * reconciliation, it must not compute money, and it must not decide bank-ness for
 * itself.
 */

function ledger(over: Partial<LedgerListItem>): LedgerListItem {
  return {
    id: "l-1",
    name: "HDFC Bank",
    group_name: "Bank Accounts",
    opening_balance: "0.0000",
    opening_dc: "D",
    maintain_bill_by_bill: false,
    gstin: "",
    state_code: "",
    registration_type: "UNKNOWN",
    address: "",
    city: "",
    pincode: "",
    // NOT RECORDED by default, which is the honest default for a fixture: most
    // existing bank ledgers have no account number, because migration 0064
    // deliberately backfills none — an account number cannot be derived.
    bank_account_number: "",
    bank_ifsc: "",
    is_system: false,
    is_bank: true,
    ...over,
  };
}

function line(over: Partial<BankReconLine>): BankReconLine {
  return {
    voucher_id: "v-1",
    leg_no: 0,
    voucher_no: "PAY-001",
    // ⚠️ THE FIXTURE'S VOUCHER NUMBERS ARE MORE DISTINGUISHABLE THAN REALITY.
    // "PAY-001"/"PAY-009" are unique on their face, which is exactly why this
    // suite never saw the collision that the demo company shows immediately:
    // numbering is per (class, FY), so a real bank ledger carries several legs
    // all numbered "1". The class is what tells them apart.
    class: "Payment",
    voucher_date: "2026-06-01",
    narration: "",
    party: "Acme Traders",
    amount: "5000.0000",
    dr_cr: "C",
    bank_date: "",
    age_days: 40,
    broken_by_alter: false,
    ...over,
  };
}

function brs(over: Partial<BankReconciliationResponse> = {}): BankReconciliationResponse {
  return {
    ledger_id: "l-1",
    ledger_name: "HDFC Bank",
    as_on: "2026-07-11",
    balance_as_per_books: "100000.0000",
    uncleared_net: "-5000.0000",
    balance_as_per_bank: "105000.0000",
    difference: "0.0000",
    balanced: true,
    broken_count: 0,
    uncleared: [],
    cleared: [],
    ...over,
  };
}

const SNAP: NavSnapshot = {
  screen: "bank-reconciliation",
  screenStackDepth: 2,
  canReturnToVoucher: false,
};

function renderScreen(
  over: Partial<CoredClient>,
  snapshot: NavSnapshot = SNAP,
) {
  const base = createMockClient({ seed: false });
  const client = { ...base, ...over } as CoredClient;
  const utils = render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: "co-1", name: "Test Co", homeStateCode: "29" }}
    >
      <ScreenActionsProvider>
        <BankReconciliationScreen snapshot={snapshot} onSelect={() => {}} />
      </ScreenActionsProvider>
    </CompanyProvider>,
  );
  return { ...utils, client };
}

/**
 * Render the screen with access to the screen-action dispatcher, so a test can
 * fire a KEY's registered handler the way AppShell does. Used to prove the print
 * key is registered here at all — the bug a browser gate caught.
 */
function renderWithDispatcher(
  over: Partial<CoredClient> = {},
  snapshot: NavSnapshot = SNAP,
) {
  const base = createMockClient({ seed: false });
  const client = {
    ...base,
    listLedgers: async () => ({ ledgers: [ledger({})] }),
    bankReconciliation: async () => brs(),
    ...over,
  } as CoredClient;
  let fireHandler: () => Promise<string | void> = async () => {};
  let fireAccept: () => { ran: boolean; message?: string } = () => ({ ran: false });

  function Harness() {
    const dispatch = useScreenActionDispatcher();
    fireHandler = async () => {
      const res = dispatch("print", snapshot);
      if (res.pending) return await res.pending;
      return res.message;
    };
    fireAccept = () => {
      const res = dispatch("accept", snapshot);
      return { ran: res.ran, message: res.message };
    };
    return <BankReconciliationScreen snapshot={snapshot} onSelect={() => {}} />;
  }

  render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: "co-1", name: "Test Co", homeStateCode: "29" }}
    >
      <ScreenActionsProvider>
        <Harness />
      </ScreenActionsProvider>
    </CompanyProvider>,
  );
  return { fireHandler: () => fireHandler(), fireAccept: () => fireAccept() };
}

describe("choosing the bank account (I-9)", () => {
  it("offers ONLY ledgers the server marked is_bank", async () => {
    // The screen must not second-guess is_bank from group_name: bank-ness is
    // ltree ancestry and group_name is the immediate group, so a filter here
    // would silently drop an account in a user sub-group. "Current Accounts"
    // below is exactly that case and MUST be offered.
    const listLedgers = vi.fn(async () => ({
      ledgers: [
        ledger({ id: "l-1", name: "HDFC Bank", group_name: "Bank Accounts" }),
        ledger({
          id: "l-2",
          name: "ICICI Current",
          group_name: "Current Accounts", // a user sub-group — still a bank
        }),
        ledger({
          id: "l-3",
          name: "Acme Traders",
          group_name: "Sundry Debtors",
          is_bank: false,
        }),
      ],
    }));
    renderScreen({ listLedgers, bankReconciliation: async () => brs() });

    // Two banks => the picker.
    expect(await screen.findByText("HDFC Bank")).toBeTruthy();
    expect(screen.getByText("ICICI Current")).toBeTruthy();
    expect(screen.queryByText("Acme Traders")).toBeNull();
  });

  it("opens the only bank account without making the operator pick from one", async () => {
    const bankReconciliation = vi.fn(async () => brs());
    renderScreen({
      listLedgers: async () => ({ ledgers: [ledger({})] }),
      bankReconciliation,
    });
    await waitFor(() => expect(bankReconciliation).toHaveBeenCalled());
    expect(bankReconciliation).toHaveBeenCalledWith("co-1", "l-1", undefined);
  });

  it("says so plainly when the company has NO bank account", async () => {
    // An explicit state, not a blank grid: the operator must be able to tell
    // "you have no bank accounts" from "this screen is broken".
    renderScreen({ listLedgers: async () => ({ ledgers: [] }) });
    expect(await screen.findByTestId("brs-no-banks")).toBeTruthy();
  });

  it("refuses a drilled ledger that is not a bank account, by name", async () => {
    renderScreen(
      {
        listLedgers: async () => ({ ledgers: [ledger({})] }),
      },
      { ...SNAP, detailOf: "ledger:Acme Traders" },
    );
    const err = await screen.findByTestId("brs-error");
    expect(err.textContent).toContain("Acme Traders");
    expect(err.textContent).toContain("not a bank account");
  });
});

describe("a voucher altered AFTER being reconciled (I-7)", () => {
  // broken_count > 0 means the difference cannot silently absorb an edit made
  // after clearing. The server flags it; a client that rendered it as an ordinary
  // row would defeat the one invariant T4 exists to protect.
  const broken = line({
    voucher_id: "v-9",
    voucher_no: "PAY-009",
    amount: "7000.0000",
    dr_cr: "C",
    broken_by_alter: true,
    cleared_amount: "5000.0000",
    cleared_dr_cr: "C",
  });

  it("warns ABOVE the rows, not inside them", async () => {
    renderScreen({
      listLedgers: async () => ({ ledgers: [ledger({})] }),
      bankReconciliation: async () =>
        brs({ broken_count: 1, uncleared: [broken] }),
    });
    const warning = await screen.findByTestId("brs-broken-warning");
    expect(warning.textContent).toMatch(/altered AFTER being reconciled/i);
    expect(warning.textContent).toMatch(/un-cleared/i);
  });

  it("shows the EVIDENCE: what was cleared, beside what it now says", async () => {
    renderScreen({
      listLedgers: async () => ({ ledgers: [ledger({})] }),
      bankReconciliation: async () =>
        brs({ broken_count: 1, uncleared: [broken] }),
    });
    const row = await screen.findByTestId("brs-broken:v-9:0");
    // Both figures must be present — the whole point is the comparison.
    expect(row.textContent).toContain("5,000");
    expect(row.textContent).toContain("7,000");
  });

  it("shows no warning when nothing is broken (the CONTROL)", async () => {
    renderScreen({
      listLedgers: async () => ({ ledgers: [ledger({})] }),
      bankReconciliation: async () => brs({ uncleared: [line({})] }),
    });
    await screen.findByTestId("brs-uncleared");
    expect(screen.queryByTestId("brs-broken-warning")).toBeNull();
  });
});

describe("the statement renders the server's numbers, and only those", () => {
  it("renders all four balances and the server's own balanced verdict", async () => {
    renderScreen({
      listLedgers: async () => ({ ledgers: [ledger({})] }),
      bankReconciliation: async () =>
        brs({
          balance_as_per_books: "100000.0000",
          uncleared_net: "-5000.0000",
          balance_as_per_bank: "105000.0000",
          difference: "0.0000",
          balanced: true,
        }),
    });
    // INDIAN grouping (lakhs), because this is an Indian ERP and formatMoney is
    // the same formatter every other statement uses: 1,00,000.00 not 100,000.00.
    expect((await screen.findByTestId("brs-books")).textContent).toBe("1,00,000.00");
    expect(screen.getByTestId("brs-per-bank").textContent).toBe("1,05,000.00");
    expect(screen.getByTestId("brs-balanced").textContent).toBe("balanced");
  });

  it("reports NOT BALANCED from the server's flag, not from parsing the string", async () => {
    // If the screen compared difference === "0.0000" itself it would disagree with
    // the server the moment either changed its formatting. The flag is authority.
    renderScreen({
      listLedgers: async () => ({ ledgers: [ledger({})] }),
      bankReconciliation: async () =>
        brs({ difference: "0.0000", balanced: false }),
    });
    expect((await screen.findByTestId("brs-balanced")).textContent).toBe(
      "NOT BALANCED",
    );
  });

  it("shows an explicit 'nothing outstanding' state, not an empty grid", async () => {
    renderScreen({
      listLedgers: async () => ({ ledgers: [ledger({})] }),
      bankReconciliation: async () => brs(),
    });
    expect((await screen.findByTestId("brs-uncleared-empty")).textContent).toMatch(
      /Nothing outstanding/i,
    );
  });

  it("never shows a bank account number or IFSC column (I-11)", async () => {
    // Not stored yet (t4-model.md §2 deferred them to slice 2). A blank labelled
    // field would read as "this bank has no IFSC".
    const { container } = renderScreen({
      listLedgers: async () => ({ ledgers: [ledger({})] }),
      bankReconciliation: async () => brs({ uncleared: [line({})] }),
    });
    await screen.findByTestId("brs-uncleared");
    expect(container.textContent).not.toMatch(/IFSC/i);
    expect(container.textContent).not.toMatch(/A\/c No/i);
  });
});

describe("ticking an item off — the one write this screen makes", () => {
  it("PUTs the keyed bank date for the right leg", async () => {
    const setBankDate = vi.fn(async () => ({ status: "reconciled" }));
    renderScreen({
      listLedgers: async () => ({ ledgers: [ledger({})] }),
      bankReconciliation: async () =>
        brs({ uncleared: [line({ voucher_id: "v-7", leg_no: 1 })] }),
      setBankDate,
    });
    const input = await screen.findByTestId("brs-bankdate:v-7:1");
    fireEvent.change(input, { target: { value: "2026-06-05" } });
    fireEvent.blur(input);
    await waitFor(() => expect(setBankDate).toHaveBeenCalled());
    // leg_no is the ORDINAL and must be passed through: a voucher with two legs
    // on the same bank account has two independently tickable rows.
    expect(setBankDate).toHaveBeenCalledWith("co-1", "l-1", "v-7", 1, {
      bank_date: "2026-06-05",
      created_by: "ui",
    });
  });

  it("clearing the cell UN-reconciles rather than sending an empty date", async () => {
    const clearBankDate = vi.fn(async () => ({ status: "unreconciled" }));
    const setBankDate = vi.fn(async () => ({ status: "reconciled" }));
    renderScreen({
      listLedgers: async () => ({ ledgers: [ledger({})] }),
      bankReconciliation: async () =>
        brs({ cleared: [line({ bank_date: "2026-06-05" })] }),
      setBankDate,
      clearBankDate,
    });
    const input = await screen.findByTestId("brs-bankdate:v-1:0");
    fireEvent.change(input, { target: { value: "" } });
    fireEvent.blur(input);
    await waitFor(() => expect(clearBankDate).toHaveBeenCalled());
    // A blank bank_date would be refused by the server (it is required), so this
    // must route to DELETE instead.
    expect(setBankDate).not.toHaveBeenCalled();
  });

  it("shows the SERVER's refusal when the bank date precedes the voucher", async () => {
    // Migration 0045's constraint trigger. The message names the field, which is
    // more useful than anything this screen could invent.
    renderScreen({
      listLedgers: async () => ({ ledgers: [ledger({})] }),
      bankReconciliation: async () => brs({ uncleared: [line({})] }),
      setBankDate: async () => {
        throw new ApiError(
          400,
          "banking: bank_date 2026-05-01 is before the voucher date 2026-06-01",
        );
      },
    });
    const input = await screen.findByTestId("brs-bankdate:v-1:0");
    fireEvent.change(input, { target: { value: "2026-05-01" } });
    fireEvent.blur(input);
    const err = await screen.findByTestId("brs-row-error");
    expect(err.textContent).toContain("before the voucher date");
  });

  // ------------------------------------------------------------------ NEW.
  // Both of these were found by DRIVING the screen against real cored, and
  // neither was reachable from this suite before, for the same root cause: the
  // Bank Date cell is an uncontrolled `defaultValue` input, which React applies
  // at MOUNT only. Whatever the operator typed therefore stayed on screen no
  // matter what the server did with it.

  it("puts the SERVER's value back after a refusal — the refused date must not linger", async () => {
    // Driven 2026-08-07: keying 2026-05-01 on a 2026-05-30 voucher returned 400
    // ("a cheque cannot clear before it is written") and the cell went on
    // showing 2026-05-01 beside the error. An operator reads a visible date as
    // a stored one; nothing had been stored.
    renderScreen({
      listLedgers: async () => ({ ledgers: [ledger({})] }),
      bankReconciliation: async () => brs({ uncleared: [line({})] }),
      setBankDate: async () => {
        throw new ApiError(
          400,
          "banking: bank_date 2026-05-01 precedes the voucher date 2026-06-01",
        );
      },
    });
    const input = (await screen.findByTestId(
      "brs-bankdate:v-1:0",
    )) as HTMLInputElement;
    fireEvent.change(input, { target: { value: "2026-05-01" } });
    fireEvent.blur(input);
    await screen.findByTestId("brs-row-error");
    const after = (await screen.findByTestId(
      "brs-bankdate:v-1:0",
    )) as HTMLInputElement;
    // The server holds NOTHING for this leg, so the cell must hold nothing.
    expect(after.value, "the refused date is still on screen").toBe("");
  });

  it("DISCLOSES a tick stored outside this statement's window", async () => {
    // Driven 2026-08-07 and confirmed in Postgres: ticking a 2026-06-05 leg with
    // a bank date of 2026-07-10 on a statement as at 2026-06-30 returns 200 and
    // writes the row — and the screen is byte-identical, because the item is
    // correctly un-cleared AS AT 2026-06-30 (bankrecon.go trap 1). A confirmed
    // write that changes nothing on screen is indistinguishable from a lost one.
    const setBankDate = vi.fn(async () => ({ status: "reconciled" }));
    renderScreen(
      {
        listLedgers: async () => ({ ledgers: [ledger({})] }),
        bankReconciliation: async () =>
          brs({ as_on: "2026-06-30", uncleared: [line({})] }),
        setBankDate,
      },
      { ...SNAP, config: { periodTo: "2026-06-30" } },
    );
    const input = await screen.findByTestId("brs-bankdate:v-1:0");
    fireEvent.change(input, { target: { value: "2026-07-10" } });
    fireEvent.blur(input);
    await waitFor(() => expect(setBankDate).toHaveBeenCalled());
    const note = await screen.findByTestId("brs-out-of-window");
    // It must name the date STORED, the statement date, and the way out.
    expect(note.textContent).toContain("2026-07-10");
    expect(note.textContent).toContain("2026-06-30");
    expect(note.textContent).toMatch(/F2/);
  });

  it("says NOTHING when the tick lands inside the window (the CONTROL)", async () => {
    // Without this control the disclosure above passes by always firing.
    const setBankDate = vi.fn(async () => ({ status: "reconciled" }));
    renderScreen(
      {
        listLedgers: async () => ({ ledgers: [ledger({})] }),
        bankReconciliation: async () =>
          brs({ as_on: "2026-06-30", uncleared: [line({})] }),
        setBankDate,
      },
      { ...SNAP, config: { periodTo: "2026-06-30" } },
    );
    const input = await screen.findByTestId("brs-bankdate:v-1:0");
    fireEvent.change(input, { target: { value: "2026-06-20" } });
    fireEvent.blur(input);
    await waitFor(() => expect(setBankDate).toHaveBeenCalled());
    expect(screen.queryByTestId("brs-out-of-window")).toBeNull();
  });

  it("names the voucher CLASS, because the number alone repeats", async () => {
    // The demo company shows FOUR legs on HDFC Bank all numbered "1". A row
    // labelled by number alone cannot be identified by an operator, and its
    // test id collides four ways for anyone driving the screen.
    renderScreen({
      listLedgers: async () => ({ ledgers: [ledger({})] }),
      bankReconciliation: async () =>
        brs({
          uncleared: [
            line({ voucher_id: "v-1", voucher_no: "1", class: "Receipt" }),
            line({ voucher_id: "v-2", voucher_no: "1", class: "Contra" }),
          ],
        }),
    });
    // Two rows sharing voucher_no "1" are each independently addressable...
    expect(await screen.findByTestId("brs-bankdate:v-1:0")).toBeTruthy();
    expect(await screen.findByTestId("brs-bankdate:v-2:0")).toBeTruthy();
    // ...and the class is on screen, which is what a HUMAN tells them apart by.
    expect(screen.getByText("Receipt")).toBeTruthy();
    expect(screen.getByText("Contra")).toBeTruthy();
  });

  it("does not write when the cell is left unchanged", async () => {
    const setBankDate = vi.fn(async () => ({ status: "reconciled" }));
    renderScreen({
      listLedgers: async () => ({ ledgers: [ledger({})] }),
      bankReconciliation: async () =>
        brs({ cleared: [line({ bank_date: "2026-06-05" })] }),
      setBankDate,
    });
    const input = await screen.findByTestId("brs-bankdate:v-1:0");
    fireEvent.blur(input);
    expect(setBankDate).not.toHaveBeenCalled();
  });
});

describe("the statement date (F2)", () => {
  it("passes the period's UPPER bound as as_on — it is a position", async () => {
    const bankReconciliation = vi.fn(async () => brs({ as_on: "2026-06-30" }));
    renderScreen(
      {
        listLedgers: async () => ({ ledgers: [ledger({})] }),
        bankReconciliation,
      },
      {
        ...SNAP,
        // periodFrom is deliberately ignored: bounding a position below would
        // drop earlier history and still balance.
        config: { periodFrom: "2026-06-01", periodTo: "2026-06-30" },
      },
    );
    await waitFor(() => expect(bankReconciliation).toHaveBeenCalled());
    expect(bankReconciliation).toHaveBeenCalledWith("co-1", "l-1", "2026-06-30");
  });
});

describe("Ctrl+P on the statement (I-10)", () => {
  it("registers a print handler and sends the SELECTED account", async () => {
    // REGRESSION, in two layers.
    //
    // The first is the original one: printTarget.ts had the reasoned refusal
    // from the start, but no screen was wired to deliver it — so the shell fell
    // through to its generic "Print does not apply on this screen", which reads
    // like a missing feature rather than a deliberate refusal. No unit test
    // caught it (they all tested the resolver directly); the browser gate did.
    // A falsy message here means the handler is not registered at all.
    //
    // The second is new with §3.4: the screen must hand its SELECTED ledger to
    // the print action. One that registers the key but drops the id gets a 400
    // and tells an operator to choose an account they already chose.
    //
    // The screen auto-selects the only bank account, so the no-selection
    // refusal is not reachable from here — it is pinned at the resolver in
    // printTarget.test.ts, which is where the decision actually lives.
    const printReport = vi.fn(async () => ({ blob: new Blob([]), filename: null }));
    const { fireHandler } = renderWithDispatcher({ printReport } as Partial<CoredClient>);
    await screen.findByTestId("brs-as-on");
    const msg = await fireHandler();
    expect(msg, "no print handler is registered on the statement").toBeTruthy();
    await waitFor(() =>
      expect(printReport).toHaveBeenCalledWith("co-1", "bank-reconciliation", {
        ledger_id: "l-1",
      }),
    );
  });
});

describe("Ctrl+A on the statement — the bar sells it, so it must answer", () => {
  /**
   * ⛔ THE SAME CLASS AS THE DEAD Ctrl+P, ON THE KEY NEXT TO IT. BANK_RECON_BAR
   * carries "Ctrl+A Accept", this screen registered no `accept` handler, and the
   * pure machine does not move on Ctrl+A from this frame — so the key answered
   * the shell's generic "Ctrl+A — Accept does not apply on this screen" while the
   * strip above the screen advertised it. Measured by walking every frame whose
   * bar sells a screen-owned key (screens/printTarget.screens.test.tsx).
   *
   * ⚠️ AND WHAT IT SAYS MATTERS MORE THAN THAT IT ANSWERS. There is no save on
   * this screen: each bank date is written as it is typed. An operator who
   * believes Ctrl+A commits their ticks would leave the screen assuming the work
   * was saved by a key that never saved anything.
   */
  it("answers, and says the ticks were already saved", async () => {
    const { fireAccept } = renderWithDispatcher();
    await screen.findByTestId("brs-as-on");
    const res = fireAccept();
    expect(
      res.ran,
      'BANK_RECON_BAR advertises "Ctrl+A Accept" and no handler is registered, so the key ' +
        "answers the shell's generic \"does not apply on this screen\"",
    ).toBe(true);
    expect(res.message).toMatch(/saved the moment it was typed/);
    // It names the account, so the sentence is about the statement in front of
    // the operator rather than about the screen in general.
    expect(res.message).toContain("HDFC Bank");
    // A BALANCED statement says so. The verdict is the SERVER'S `balanced`, never
    // a comparison done here — the same rule the Difference row follows.
    expect(res.message).toMatch(/balances/);
  });

  it("an UNBALANCED statement is not reported as accepted", async () => {
    // The control. "Nothing to accept" on its own would read as "all is well" on
    // the one screen where a non-zero difference is the whole point.
    const { fireAccept } = renderWithDispatcher({
      bankReconciliation: async () =>
        brs({ difference: "-1250.5000", balanced: false }),
    } as Partial<CoredClient>);
    await screen.findByTestId("brs-as-on");
    const res = fireAccept();
    expect(res.ran).toBe(true);
    expect(res.message).toMatch(/does NOT balance/);
    // The figure is the server's string, formatted for display and never
    // recomputed here.
    expect(res.message).toContain("1,250.50");
  });
});

describe("a non-bank ledger reaching the route anyway", () => {
  it("shows the server's refusal rather than an empty statement", async () => {
    // Defence in depth: the picker should make this unreachable, but the refusal
    // stays. An empty statement would read as "nothing to reconcile".
    renderScreen({
      listLedgers: async () => ({ ledgers: [ledger({})] }),
      bankReconciliation: async () => {
        throw new ApiError(
          400,
          'reporting: ledger "Acme Traders" is not under a bank group; a bank reconciliation statement needs a bank ledger',
        );
      },
    });
    const err = await screen.findByTestId("brs-error");
    expect(err.textContent).toContain("not under a bank group");
  });
});

describe("Ctrl+P says WHICH account and WHICH date went to paper", () => {
  /**
   * ⛔ THE REFUSAL THAT CANNOT FIRE HERE. printTarget refuses to print a
   * statement for an account nobody chose — but this screen opens the only bank
   * account on load, so on a one-bank company Ctrl+P as the FIRST key prints an
   * account the operator was never asked about. That auto-selection is right
   * (a pick-list of one is friction); doing it silently is not. So the
   * confirmation names the account, and the operator can see at once that it is
   * not the one they meant.
   */
  it("on a TWO-bank company the refusal IS reached, and makes no request", async () => {
    // ⛔ THE OTHER HALF OF THE SAME ROW, AND IT IS WHAT KEEPS THE REFUSAL HONEST.
    // The register said the "select a bank account first" refusal is unreachable
    // because the screen auto-selects — true only for a company with exactly ONE
    // bank account. With two, the screen opens the PICKER and chooses nothing, so
    // Ctrl+P before choosing must refuse rather than print "the first one": a
    // real, correctly rendered statement for the wrong bank, with nothing on the
    // page saying so.
    const printReport = vi.fn(async () => ({ blob: new Blob([]), filename: null }));
    const { fireHandler } = renderWithDispatcher({
      listLedgers: async () => ({
        ledgers: [
          ledger({ id: "l-1", name: "HDFC Bank" }),
          ledger({ id: "l-2", name: "ICICI Current" }),
        ],
      }),
      printReport,
    } as Partial<CoredClient>);
    // The picker, not a statement: nothing was chosen for the operator.
    await screen.findByText(/HDFC Bank/);
    const msg = String(await fireHandler());
    expect(msg).toContain("Select a bank account first");
    // A refusal that still hits the network is a 400 in somebody's log — and on
    // this route it would be a request for a statement nobody asked for.
    expect(printReport).not.toHaveBeenCalled();
  });

  it("names the auto-selected account in the confirmation", async () => {
    const printReport = vi.fn(async () => ({
      blob: new Blob(["%PDF-1.4"], { type: "application/pdf" }),
      filename: "bank-reconciliation-20260711.pdf",
    }));
    const { fireHandler } = renderWithDispatcher({
      listLedgers: async () => ({ ledgers: [ledger({ name: "HDFC Bank" })] }),
      printReport,
    } as Partial<CoredClient>);
    await screen.findByTestId("brs-as-on");
    const msg = String(await fireHandler());
    expect(msg).toContain("HDFC Bank");
  });

  it("prints AS AT the F2 date the screen is showing, not the latest date in the books", async () => {
    // ⛔ THE STATEMENT IS A POSITION. The server treats a missing as_on as "the
    // latest date in the books", so without this the printed BRS reconciles a
    // DIFFERENT day from the one on screen — on the one document in the product
    // a bank manager is shown.
    const printReport = vi.fn(async () => ({
      blob: new Blob(["%PDF-1.4"], { type: "application/pdf" }),
      filename: "bank-reconciliation-20260630.pdf",
    }));
    const dated: NavSnapshot = { ...SNAP, config: { periodTo: "2026-06-30" } };
    const { fireHandler } = renderWithDispatcher(
      { printReport } as Partial<CoredClient>,
      dated,
    );
    await screen.findByTestId("brs-as-on");
    await fireHandler();
    await waitFor(() =>
      expect(printReport).toHaveBeenCalledWith("co-1", "bank-reconciliation", {
        ledger_id: "l-1",
        as_on: "2026-06-30",
      }),
    );
  });
});
