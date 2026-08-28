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
  BankMatchesResponse,
  BankMatchProposal,
  BankReconciliationResponse,
  LedgerListItem,
  SetAsideRequest,
  SetBankDateRequest,
} from "../api";
import type { ScreenActionKind } from "../app/ScreenActions";
import type { NavSnapshot } from "../keystroke/navMachine";

/**
 * BANK STATEMENT MATCHING (T4 slice 2b) — the Alt+S panel.
 *
 * Design memo: docs/design/t4-slice2-model.md §0, §4, §5.
 *
 * ⛔ WHAT THIS SUITE GUARDS is not that the panel renders. It is that the panel
 * cannot undo the three things the slice rests on:
 *
 *  1. A PROPOSAL IS NOT A RECONCILIATION. Nothing is written on load, an
 *     AMBIGUOUS row is never offered for one-key acceptance, and the two lists
 *     are worded so they cannot be confused.
 *  2. SET ASIDE IS NOT A WEAK MATCH. It writes no bank date, and the screen says
 *     so in those words.
 *  3. WITHDRAWING A MATCH WITHDRAWS ITS CLEARANCE, and the key says so before it
 *     does — un-linking and un-ticking are one operation, not two.
 */

const SNAP: NavSnapshot = {
  screen: "bank-reconciliation",
  screenStackDepth: 2,
  canReturnToVoucher: false,
};

function ledger(over: Partial<LedgerListItem> = {}): LedgerListItem {
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
    pan: "",
    credit_days: null,
    address: "",
    city: "",
    pincode: "",
    bank_account_number: "50200012345678",
    bank_ifsc: "HDFC0004321",
    is_system: false,
    is_bank: true,
    ...over,
  };
}

function brs(): BankReconciliationResponse {
  return {
    ledger_id: "l-1",
    ledger_name: "HDFC Bank",
    as_on: "2026-07-11",
    balance_as_per_books: "100000.0000",
    uncleared_net: "0.0000",
    balance_as_per_bank: "100000.0000",
    difference: "0.0000",
    balanced: true,
    broken_count: 0,
    uncleared: [],
    cleared: [],
  };
}

/** An unambiguous EXACT proposal — the one row a bulk accept may touch. */
function exactRow(): BankMatchProposal {
  return {
    line_id: "line-exact",
    line_no: 1,
    value_date: "2026-05-14",
    description: "RENT MAY",
    amount: "1000.0000",
    dr_cr: "C",
    best_tier: "exact",
    ambiguous: false,
    acceptable_in_bulk: true,
    candidates: [
      {
        voucher_id: "v-1",
        leg_no: 0,
        voucher_no: "PAY-1",
        class: "Payment",
        voucher_date: "2026-05-14",
        narration: "rent",
        party: "Landlord",
        amount: "1000.0000",
        dr_cr: "C",
        tier: "exact",
        why: "amount, direction and date all agree to the paisa and the day",
      },
    ],
  };
}

/** ⛔ INVARIANT B-1: one bank row, two vouchers of the same amount and day. */
function ambiguousRow(): BankMatchProposal {
  return {
    line_id: "line-ambig",
    line_no: 2,
    value_date: "2026-05-14",
    description: "TRANSFER",
    amount: "25000.0000",
    dr_cr: "C",
    best_tier: "exact",
    ambiguous: true,
    acceptable_in_bulk: false,
    candidates: [
      {
        voucher_id: "v-2",
        leg_no: 0,
        voucher_no: "PAY-2",
        class: "Payment",
        voucher_date: "2026-05-14",
        narration: "to Acme",
        party: "Acme",
        amount: "25000.0000",
        dr_cr: "C",
        tier: "exact",
        why: "amount, direction and date all agree",
      },
      {
        voucher_id: "v-3",
        leg_no: 0,
        voucher_no: "PAY-3",
        class: "Payment",
        voucher_date: "2026-05-14",
        narration: "to Globex",
        party: "Globex",
        amount: "25000.0000",
        dr_cr: "C",
        tier: "exact",
        why: "amount, direction and date all agree",
      },
    ],
  };
}

/** A bank row nothing in the books explains — the set-aside candidate. */
function orphanRow(): BankMatchProposal {
  return {
    line_id: "line-orphan",
    line_no: 3,
    value_date: "2026-05-20",
    description: "ACCOUNT MAINTENANCE FEE",
    amount: "177.0000",
    dr_cr: "C",
    best_tier: "",
    ambiguous: false,
    acceptable_in_bulk: false,
    candidates: [],
  };
}

/**
 * Typed spies. They are declared with the client's own parameter lists rather
 * than bare `vi.fn()` so a call-site assertion reads REAL argument names — a
 * spy typed `[]` would let `calls[0][4].bank_date` compile against nothing and
 * assert nothing.
 */
const spySetBankDate = () =>
  vi.fn(
    async (
      _companyID: string,
      _ledgerID: string,
      _voucherID: string,
      _legNo: number,
      _req: SetBankDateRequest,
    ) => ({ status: "reconciled" as const }),
  );
const spyClearBankDate = () =>
  vi.fn(
    async (
      _companyID: string,
      _ledgerID: string,
      _voucherID: string,
      _legNo: number,
    ) => ({ status: "unreconciled" as const }),
  );
const spySetAside = () =>
  vi.fn(
    async (
      _companyID: string,
      _ledgerID: string,
      _lineID: string,
      _req: SetAsideRequest,
    ) => ({ status: "set-aside" as const }),
  );

const NOTHING_RECONCILED =
  "NOTHING ON THIS SCREEN HAS BEEN RECONCILED. Every row below is a PROPOSAL; " +
  "a bank date is written only when you accept one, and accepting is the assertion " +
  "that this bank row IS that voucher.";

function matches(over: Partial<BankMatchesResponse> = {}): BankMatchesResponse {
  const proposals = over.proposals ?? [exactRow(), ambiguousRow(), orphanRow()];
  return {
    ledger_id: "l-1",
    ledger_name: "HDFC Bank",
    from: "",
    to: "",
    date_window_days: 30,
    proposals,
    unmatched_legs: [],
    resolved: [],
    counts: {
      lines: proposals.length,
      exact: proposals.filter((p) => p.best_tier === "exact").length,
      potential: 0,
      partial: 0,
      ambiguous: proposals.filter((p) => p.ambiguous).length,
      no_candidate: proposals.filter((p) => p.best_tier === "").length,
      acceptable_in_bulk: proposals.filter((p) => p.acceptable_in_bulk).length,
      unmatched_legs: 0,
    },
    disclosures: [NOTHING_RECONCILED],
    ...over,
  };
}

/**
 * Render the screen and expose a way to fire any registered screen action the
 * way AppShell does — which also proves the key is registered on this screen.
 */
function renderScreen(over: Partial<CoredClient> = {}) {
  const base = createMockClient({ seed: false });
  const client = {
    ...base,
    listLedgers: async () => ({ ledgers: [ledger()] }),
    bankReconciliation: async () => brs(),
    bankStatementMatches: async () => matches(),
    ...over,
  } as CoredClient;

  let fire: (k: ScreenActionKind) => Promise<string | void> = async () => {};
  function Harness() {
    const dispatch = useScreenActionDispatcher();
    fire = async (kind) => {
      const res = dispatch(kind, SNAP);
      if (res.pending) return await res.pending;
      return res.message;
    };
    return <BankReconciliationScreen snapshot={SNAP} onSelect={() => {}} />;
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
  return { fire: (k: ScreenActionKind) => fire(k), client };
}

/** Open the matching panel through Alt+S, as the shell would. */
async function openPanel(over: Partial<CoredClient> = {}) {
  const h = renderScreen(over);
  await screen.findByText(/HDFC Bank/);
  await h.fire("matchBankRows");
  await screen.findByTestId("match-disclosures");
  return h;
}

describe("the matching panel — a proposal is not a reconciliation", () => {
  it("says in its first disclosure that nothing has been reconciled", async () => {
    await openPanel();
    // ⛔ THE MOST DANGEROUS POSSIBLE MISREADING of this screen is that these
    // rows are done. The sentence is not decoration and must not be collapsible.
    expect(screen.getByTestId("match-disclosures").textContent).toContain(
      "NOTHING ON THIS SCREEN HAS BEEN RECONCILED",
    );
  });

  it("writes NOTHING on load — deriving proposals is a read", async () => {
    const setBankDate = spySetBankDate();
    await openPanel({ setBankDate } as Partial<CoredClient>);
    // Not "no clearance appeared": the WRITE was never called. A panel that
    // auto-accepted its one exact match would still look right on screen.
    expect(setBankDate).not.toHaveBeenCalled();
  });

  it("⛔ B-1: an ambiguous row surfaces BOTH candidates and neither is chosen", async () => {
    await openPanel();
    const row = screen.getByTestId("proposal-2");
    expect(screen.getByTestId("ambiguous-2").textContent).toContain("AMBIGUOUS");
    // Both vouchers are on screen — the operator chooses, or nobody does.
    expect(row.textContent).toContain("PAY-2");
    expect(row.textContent).toContain("PAY-3");
  });

  it("⛔ B-1: the bulk key offers ONLY the rows the server marked acceptable", async () => {
    await openPanel();
    // One exact row is bulk-acceptable; the ambiguous one is NOT, even though
    // its best tier is also "exact". The count comes from the server's flag and
    // is never re-derived from the candidate list here.
    expect(screen.getByTestId("accept-all").textContent).toContain("Accept 1");
  });

  it("accepting one proposal asserts THAT row is THAT voucher, with the bank's own date", async () => {
    const setBankDate = spySetBankDate();
    await openPanel({ setBankDate } as Partial<CoredClient>);

    fireEvent.click(screen.getByTestId("accept-1-PAY-1"));
    await waitFor(() => expect(setBankDate).toHaveBeenCalled());

    const [, ledgerID, voucherID, legNo, req] = setBankDate.mock.calls[0]!;
    expect(ledgerID).toBe("l-1");
    expect(voucherID).toBe("v-1");
    expect(legNo).toBe(0);
    // ⛔ THE DATE IS THE STATEMENT ROW'S OWN, never today and never the
    // voucher's: the server refuses any other, and it is what the back-dated
    // cutoff judges.
    expect(req.bank_date).toBe("2026-05-14");
    // ⭐ AND IT TRAVELS ON THE RECONCILIATION WRITE, not a matching route of its
    // own — which is what stops matching becoming a second way to acquire the
    // authority to assert a clearance.
    expect(req.statement_line_id).toBe("line-exact");
  });

  it("a bulk accept touches the acceptable row and no other", async () => {
    const setBankDate = spySetBankDate();
    await openPanel({ setBankDate } as Partial<CoredClient>);

    fireEvent.click(screen.getByTestId("accept-all"));
    await waitFor(() => expect(setBankDate).toHaveBeenCalledTimes(1));
    expect(setBankDate.mock.calls[0]![4].statement_line_id).toBe("line-exact");
  });
});

describe("set aside — a real category, not a weak match", () => {
  it("F9 refuses to guess which row it is about", async () => {
    const h = await openPanel();
    // No row selected. ⛔ A key that acted on "the first row" would set aside a
    // row the operator never looked at.
    const msg = await h.fire("setAsideBankRow");
    expect(String(msg)).toContain("Select a bank row first");
  });

  it("F9 sets the SELECTED row aside and writes no bank date", async () => {
    const setBankDate = spySetBankDate();
    const setAsideStatementLine = spySetAside();
    const h = await openPanel({
      setBankDate,
      setAsideStatementLine,
    } as Partial<CoredClient>);

    fireEvent.click(screen.getByTestId("proposal-3"));
    const msg = await h.fire("setAsideBankRow");

    await waitFor(() => expect(setAsideStatementLine).toHaveBeenCalled());
    expect(setAsideStatementLine.mock.calls[0]![2]).toBe("line-orphan");
    // ⛔ THE ASSERTION THAT MATTERS: no clearance was written.
    expect(setBankDate).not.toHaveBeenCalled();
    expect(String(msg)).toContain("Nothing was reconciled");
  });

  it("renders a set-aside row so it cannot be read as reconciled", async () => {
    await openPanel({
      bankStatementMatches: async () =>
        matches({
          proposals: [],
          resolved: [
            {
              line_id: "line-orphan",
              status: "set-aside",
              tier: "manual",
              note: "bank charge",
              matched_by: "ui",
              matched_at: "2026-05-20T10:00:00Z",
              line_value_date: "2026-05-20",
            },
          ],
        }),
    } as Partial<CoredClient>);

    const row = screen.getByTestId("resolved-line-orphan");
    expect(row.textContent).toContain("SET ASIDE");
    expect(row.textContent).toContain("matches nothing in the books");
    // ⛔ IT MUST SAY SO IN WORDS. A status chip beside a matched row would let
    // "decided" read as "reconciled", and the difference is the whole feature.
    expect(row.textContent).toContain("Not reconciled; no bank date written");
    expect(row.textContent).not.toContain("MATCHED");
  });
});

describe("withdrawing a match", () => {
  const matched = {
    line_id: "line-exact",
    status: "matched" as const,
    tier: "exact" as const,
    matched_by: "ui",
    matched_at: "2026-05-14T10:00:00Z",
    voucher_id: "v-1",
    leg_no: 0,
    voucher_no: "PAY-1",
    class: "Payment",
    bank_date: "2026-05-14",
    line_value_date: "2026-05-14",
  };

  it("Alt+F8 withdraws the CLEARANCE, which is the same write as un-ticking", async () => {
    const clearBankDate = spyClearBankDate();
    const h = await openPanel({
      clearBankDate,
      bankStatementMatches: async () =>
        matches({ proposals: [], resolved: [matched] }),
    } as Partial<CoredClient>);

    // The button is what an operator reaches for, and Alt+F8 mirrors it.
    fireEvent.click(screen.getByTestId("unlink-line-exact"));

    await waitFor(() => expect(clearBankDate).toHaveBeenCalled());
    // ⭐ THE VOUCHER LEG, not a match id: there is no separate unlink route, so
    // a match cannot outlive the clearance it produced.
    const call = clearBankDate.mock.calls[0]!;
    expect([call[1], call[2], call[3]]).toEqual(["l-1", "v-1", 0]);
    void h;
  });

  it("Alt+F8 refuses on a row that is not matched, rather than guessing", async () => {
    const h = await openPanel();
    const msg = await h.fire("unlinkBankMatch");
    expect(String(msg)).toContain("Select a MATCHED bank row first");
  });

  it("F8 says it re-matches by WITHDRAWING first, rather than pretending to edit", async () => {
    const clearBankDate = spyClearBankDate();
    const h = await openPanel({
      clearBankDate,
      bankStatementMatches: async () =>
        matches({ proposals: [], resolved: [matched] }),
    } as Partial<CoredClient>);

    fireEvent.click(screen.getByTestId("resolved-line-exact"));
    const msg = await h.fire("modifyBankMatch");
    // There is no way to re-point a clearance without withdrawing the one that
    // exists, and the key says so instead of implying an in-place edit.
    expect(String(msg)).toContain("withdrawn");
  });

  it("surfaces a bank date that was changed AFTER the match", async () => {
    await openPanel({
      bankStatementMatches: async () =>
        matches({
          proposals: [],
          resolved: [
            {
              ...matched,
              bank_date: "2026-05-19",
              bank_date_differs: true,
            },
          ],
        }),
    } as Partial<CoredClient>);

    // Silence here would leave the screen showing a date that is not on the
    // statement it claims to come from — the same class as BROKEN BY ALTER.
    const row = screen.getByTestId("resolved-line-exact");
    expect(row.textContent).toContain("changed since this was matched");
    expect(row.textContent).toContain("2026-05-19");
    // BOTH dates are shown — the tick's and the statement's — so the reader is
    // not asked to trust one of them.
    expect(row.textContent).toContain("2026-05-14");
  });
});

describe("the exception report and the basis of values", () => {
  it("Ctrl+J counts what is outstanding, from the SAME payload the list renders", async () => {
    const h = await openPanel();
    await h.fire("bankExceptionReport");
    const panel = await screen.findByTestId("match-exceptions");
    // One orphan, one ambiguous — the server's counts, not a second derivation.
    expect(panel.textContent).toContain("ambiguous");
    expect(panel.textContent).toContain("books-only unreconciled");
  });

  it("Ctrl+B states the tolerance and calls the window a SEARCH WIDTH", async () => {
    const h = await openPanel();
    await h.fire("bankBasisOfValues");
    const panel = await screen.findByTestId("match-basis");
    expect(panel.textContent).toContain("to the paisa, with zero tolerance");
    // ⛔ THE WINDOW MUST NOT READ AS A RULE FROM ANYWHERE. It is this build's
    // search width, and saying so is what keeps it from becoming an invented
    // threshold — HANDOFF-FEATURE-LOCK.md §1.1.
    expect(panel.textContent).toContain("search width");
    // ⭐ 30 IS TALLY'S OWN DOCUMENTED DEFAULT for this exact parameter ("Find
    // Potential Matches within"). It replaced an invented 7 on 2026-08-28.
    expect(panel.textContent).toContain("30 days");
  });

  it("renders the window the SERVER used, not a constant of its own", async () => {
    const h = await openPanel({
      bankStatementMatches: async () => matches({ date_window_days: 45 }),
    } as Partial<CoredClient>);
    await h.fire("bankBasisOfValues");
    const panel = await screen.findByTestId("match-basis");
    // ⛔ A PANEL THAT PRINTED ITS OWN DEFAULT would state one window while the
    // proposals were computed under another, and every row would look right.
    // ⚠️ 45 IS DELIBERATELY NOT THE DEFAULT. When the default was 7 this test
    // contrasted against 30 — which then BECAME the default, at which point the
    // contrast would have proved nothing.
    expect(panel.textContent).toContain("45 days");
    expect(panel.textContent).not.toContain("30 days");
  });
});

describe("the keys refuse before opening onto nothing", () => {
  it("Alt+S refuses with no bank account selected", async () => {
    const h = renderScreen({ listLedgers: async () => ({ ledgers: [] }) });
    await waitFor(() => expect(screen.queryByText(/HDFC/)).toBeNull());
    const msg = await h.fire("matchBankRows");
    expect(String(msg)).toContain("Select a bank account first");
  });

  it("F9 refuses when the matching panel is not open", async () => {
    const h = renderScreen();
    await screen.findByText(/HDFC Bank/);
    const msg = await h.fire("setAsideBankRow");
    expect(String(msg)).toContain("Open the matching panel first");
  });
});
