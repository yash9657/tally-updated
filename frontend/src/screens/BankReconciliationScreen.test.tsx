import { describe, it, expect, vi } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { CompanyProvider } from "../app/CompanyContext";
import { ScreenActionsProvider } from "../app/ScreenActions";
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
    const row = await screen.findByTestId("brs-broken:PAY-009:0");
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
    const input = await screen.findByTestId("brs-bankdate:PAY-001:1");
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
    const input = await screen.findByTestId("brs-bankdate:PAY-001:0");
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
    const input = await screen.findByTestId("brs-bankdate:PAY-001:0");
    fireEvent.change(input, { target: { value: "2026-05-01" } });
    fireEvent.blur(input);
    const err = await screen.findByTestId("brs-row-error");
    expect(err.textContent).toContain("before the voucher date");
  });

  it("does not write when the cell is left unchanged", async () => {
    const setBankDate = vi.fn(async () => ({ status: "reconciled" }));
    renderScreen({
      listLedgers: async () => ({ ledgers: [ledger({})] }),
      bankReconciliation: async () =>
        brs({ cleared: [line({ bank_date: "2026-06-05" })] }),
      setBankDate,
    });
    const input = await screen.findByTestId("brs-bankdate:PAY-001:0");
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
