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
  BankReconciliationResponse,
  ImportedStatementResponse,
  ImportStatementRequest,
  ImportStatementResponse,
  LedgerListItem,
  PreviewStatementResponse,
} from "../api";
import type { NavSnapshot } from "../keystroke/navMachine";

/**
 * BANK STATEMENT IMPORT (T4 slice 2a) — the Alt+O panel.
 *
 * Design memo: docs/design/t4-slice2-model.md §8.
 *
 * WHAT THIS SUITE GUARDS is not that the panel renders. It is that the panel
 * does not undo the three decisions the slice rests on:
 *
 *  1. The operator CONFIRMS the reading. Nothing may be importable while a
 *     required column is unmapped, and an ambiguity the server refused to settle
 *     must be visible rather than silently pre-filled.
 *  2. A CHECK THAT DID NOT RUN must not look like a check that passed.
 *  3. An unrecorded account number reads as the WORDS "not recorded" — never as
 *     a blank labelled field, which says "this bank has no IFSC" (I-11).
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
    // null, not 0: "no term agreed" is not "due immediately".
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

/** A fully determined proposal — the happy path. */
function preview(
  over: Partial<PreviewStatementResponse> = {},
): PreviewStatementResponse {
  return {
    file_name: "hdfc.csv",
    header: ["Value Date", "Narration", "Withdrawal Amt.", "Deposit Amt."],
    mapping: {
      encoding: "two-columns",
      date_layout: "dd/mm/yyyy",
      value_date: "Value Date",
      txn_date: "",
      description: "Narration",
      deposit: "Deposit Amt.",
      withdrawal: "Withdrawal Amt.",
      amount: "",
      direction: "",
      running_balance: "",
      bank_reference: "",
      cheque_number: "",
    },
    unresolved: [],
    notes: [],
    known_date_layouts: ["dd/mm/yyyy", "mm/dd/yyyy", "yyyy-mm-dd"],
    known_encodings: ["two-columns", "signed-amount", "amount-and-flag"],
    ...over,
  };
}

function importResult(
  over: Partial<ImportStatementResponse> = {},
): ImportStatementResponse {
  return {
    import_id: "imp-1",
    already_imported: false,
    // The name the SERVER stored, echoed back — see D111. A fixture that
    // omitted it would let the panel's own display of it go untested.
    file_name: "HDFC-april.csv",
    lines_in_file: 2,
    new_lines: 2,
    duplicate_lines: 0,
    opening_balance: "250000.0000",
    closing_balance: "325000.0000",
    balance_walked: true,
    period_from: "2026-04-15",
    period_to: "2026-04-18",
    disclosures: [],
    ...over,
  };
}

function imported(
  over: Partial<ImportedStatementResponse> = {},
): ImportedStatementResponse {
  return {
    ledger_id: "l-1",
    ledger_name: "HDFC Bank",
    bank_account_number: "50200012345678",
    bank_ifsc: "HDFC0004321",
    from: "",
    to: "",
    imports: [],
    lines: [],
    total_in: "0.0000",
    total_out: "0.0000",
    disclosures: ["THIS REPORT SHOWS NO MATCH STATUS (T4 slice 2b)."],
    ...over,
  };
}

/**
 * Render the screen and expose a way to fire Alt+O's registered handler the way
 * AppShell does — which also proves the key is registered on this screen at all.
 */
function renderPanel(over: Partial<CoredClient> = {}) {
  const base = createMockClient({ seed: false });
  const client = {
    ...base,
    listLedgers: async () => ({ ledgers: [ledger()] }),
    bankReconciliation: async () => brs(),
    previewBankStatement: async () => preview(),
    importBankStatement: async () => importResult(),
    importedStatement: async () => imported(),
    ...over,
  } as CoredClient;

  let fire: () => Promise<string | void> = async () => {};
  function Harness() {
    const dispatch = useScreenActionDispatcher();
    fire = async () => {
      const res = dispatch("importBankStatement", SNAP);
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
  return { fire: () => fire(), client };
}

/** Choose a file without a real File dialog. */
function chooseFile(csv: string, name = "hdfc.csv") {
  const input = screen.getByTestId("stmt-file") as HTMLInputElement;
  const file = new File([csv], name, { type: "text/csv" });
  // jsdom's File has no .text() in every version; the panel awaits it, so give
  // it one deterministically rather than relying on the environment.
  Object.defineProperty(file, "text", { value: async () => csv });
  fireEvent.change(input, { target: { files: [file] } });
}

const CSV =
  "Value Date,Narration,Withdrawal Amt.,Deposit Amt.\n" +
  '15/04/2026,NEFT ACME,,"1,00,000.00"\n';

describe("Alt+O reaches the import panel", () => {
  it("opens the panel for the selected account", async () => {
    const { fire } = renderPanel();
    expect(await screen.findByTestId("brs-as-on")).toBeTruthy();
    const msg = await fire();
    expect(msg).toMatch(/Import a bank statement/i);
    expect(await screen.findByTestId("stmt-import-panel")).toBeTruthy();
  });

  it("REFUSES before opening when no account is selected, rather than opening onto nothing", async () => {
    // Two bank accounts and no drill: the screen shows the picker and has no
    // ledger selected. An import needs an account, so the key must say so.
    const { fire } = renderPanel({
      listLedgers: async () => ({
        ledgers: [ledger(), ledger({ id: "l-2", name: "ICICI Current" })],
      }),
    });
    expect(await screen.findByText("SELECT A BANK ACCOUNT")).toBeTruthy();
    const msg = await fire();
    expect(msg).toMatch(/Select a bank account first/i);
    expect(screen.queryByTestId("stmt-import-panel")).toBeNull();
  });
});

describe("the operator confirms the reading", () => {
  it("shows what the server would not settle, and will not import until it is answered", async () => {
    // The server proposed no date layout because every date in the file fits
    // dd/mm AND mm/dd. THIS IS THE CASE THAT MATTERS: choosing wrongly moves the
    // whole statement by up to eleven months and every amount stays plausible.
    const ambiguous = preview({
      mapping: { ...preview().mapping, date_layout: "" },
      unresolved: [
        "date_layout: dd/mm/yyyy,mm/dd/yyyy ALL fit every date in \"Value Date\", so the file cannot say which it means — choosing wrongly moves the whole statement by up to eleven months. Pick one",
      ],
    });
    const { fire } = renderPanel({
      previewBankStatement: async () => ambiguous,
    });
    expect(await screen.findByTestId("brs-as-on")).toBeTruthy();
    await fire();
    await waitFor(() => screen.getByTestId("stmt-file"));
    chooseFile(CSV);

    expect(await screen.findByTestId("stmt-unresolved")).toBeTruthy();
    expect(screen.getByTestId("stmt-unresolved").textContent).toMatch(
      /eleven months/,
    );

    // AND THE IMPORT IS BLOCKED. A panel that showed the warning and imported
    // anyway would be worse than one that showed nothing: the operator would
    // have read the warning and been overruled.
    const button = screen.getByTestId("stmt-import") as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    expect(screen.getByTestId("stmt-missing").textContent).toMatch(
      /date layout/i,
    );

    // Answering it unblocks the import — the control, without which this test
    // would pass against a button that is disabled for ever.
    fireEvent.change(screen.getByTestId("stmt-date-layout"), {
      target: { value: "dd/mm/yyyy" },
    });
    await waitFor(() =>
      expect((screen.getByTestId("stmt-import") as HTMLButtonElement).disabled)
        .toBe(false),
    );
  });

  it("sends the CONFIRMED mapping, not the proposal", async () => {
    // The operator overrides the proposed description column. What reaches the
    // server must be what they chose.
    // Typed with its real signature so the assertion below can index the third
    // argument — an untyped vi.fn() infers a zero-arg call tuple and `calls[0][2]`
    // becomes a compile error rather than a test.
    const importBankStatement: CoredClient["importBankStatement"] = vi.fn(
      async () => importResult(),
    );
    const { fire } = renderPanel({ importBankStatement });
    expect(await screen.findByTestId("brs-as-on")).toBeTruthy();
    await fire();
    await waitFor(() => screen.getByTestId("stmt-file"));
    chooseFile(CSV);
    await waitFor(() => screen.getByTestId("stmt-import"));

    fireEvent.change(screen.getByTestId("stmt-map-description"), {
      target: { value: "Value Date" },
    });
    fireEvent.click(screen.getByTestId("stmt-import"));

    await waitFor(() => expect(importBankStatement).toHaveBeenCalled());
    const req = vi.mocked(importBankStatement).mock.calls[0]![2];
    expect(req.mapping.description).toBe("Value Date");
  });
});

describe("a check that did not run must not look like one that passed", () => {
  it("renders the un-walked case as a warning, and the walked case as a pass", async () => {
    const { fire } = renderPanel({
      importBankStatement: async () =>
        importResult({
          balance_walked: false,
          disclosures: [
            "This file carries no running balance, so the balance walk could not run.",
          ],
        }),
    });
    expect(await screen.findByTestId("brs-as-on")).toBeTruthy();
    await fire();
    await waitFor(() => screen.getByTestId("stmt-file"));
    chooseFile(CSV);
    await waitFor(() => screen.getByTestId("stmt-import"));
    fireEvent.click(screen.getByTestId("stmt-import"));

    expect(await screen.findByTestId("stmt-walk")).toBeTruthy();
    const walk = screen.getByTestId("stmt-walk");
    expect(walk.textContent).toMatch(/DID NOT RUN/);
    // Not merely different words — a DIFFERENT WEIGHT on the page. An operator
    // skims; "did not run" set in the same grey as "passed" is not a warning.
    expect(walk.className).toMatch(/font-bold/);
    expect(screen.getByTestId("stmt-disclosures").textContent).toMatch(
      /could not run/,
    );
  });
});

describe("W5 — the mandatory balance proof", () => {
  it("sends the keyed statement balances as STRINGS and says a totals proof is not a walk", async () => {
    const importBankStatement: CoredClient["importBankStatement"] = vi.fn(
      async () =>
        importResult({
          balance_walked: false,
          balance_proof: "declared",
          balance_declared_by: "operator",
        }),
    );
    const { fire } = renderPanel({ importBankStatement });
    expect(await screen.findByTestId("brs-as-on")).toBeTruthy();
    await fire();
    await waitFor(() => screen.getByTestId("stmt-file"));
    chooseFile(CSV);
    await waitFor(() => screen.getByTestId("stmt-import"));

    fireEvent.change(screen.getByTestId("stmt-declared-opening"), {
      target: { value: "2,50,000.00" },
    });
    fireEvent.change(screen.getByTestId("stmt-declared-closing"), {
      target: { value: "325000.00" },
    });
    fireEvent.click(screen.getByTestId("stmt-import"));

    await waitFor(() => expect(importBankStatement).toHaveBeenCalled());
    const req = vi.mocked(importBankStatement).mock.calls[0]![2];
    // Verbatim strings: the proof is the SERVER's arithmetic, in decimals.
    expect(req.declared_opening_balance).toBe("2,50,000.00");
    expect(req.declared_closing_balance).toBe("325000.00");

    const walk = await screen.findByTestId("stmt-walk");
    expect(walk.textContent).toMatch(/PROVED by totals, not row by row/);
    expect(walk.textContent).not.toMatch(/PASSED/);
    // Still set apart from a pass: a weaker proof is not a walk.
    expect(walk.className).toMatch(/font-bold/);
  });

  it("sends no declared balance at all when none was keyed", async () => {
    const importBankStatement: CoredClient["importBankStatement"] = vi.fn(
      async () => importResult(),
    );
    const { fire } = renderPanel({ importBankStatement });
    expect(await screen.findByTestId("brs-as-on")).toBeTruthy();
    await fire();
    await waitFor(() => screen.getByTestId("stmt-file"));
    chooseFile(CSV);
    await waitFor(() => screen.getByTestId("stmt-import"));
    fireEvent.click(screen.getByTestId("stmt-import"));
    await waitFor(() => expect(importBankStatement).toHaveBeenCalled());
    const req = vi.mocked(importBankStatement).mock.calls[0]![2];
    // Absent, not "": an empty string would be a declared balance of nothing.
    expect(req).not.toHaveProperty("declared_opening_balance", "");
    expect(req.declared_opening_balance).toBeUndefined();
    expect(req.declared_closing_balance).toBeUndefined();
  });
});

describe("the account identity (I-11)", () => {
  it("says NOT RECORDED in words when the ledger has no account number", async () => {
    const { fire } = renderPanel({
      listLedgers: async () => ({
        ledgers: [ledger({ bank_account_number: "", bank_ifsc: "" })],
      }),
    });
    expect(await screen.findByTestId("brs-as-on")).toBeTruthy();
    await fire();
    expect(await screen.findByTestId("stmt-account-identity")).toBeTruthy();
    // The WORDS, not a blank: a blank labelled field reads as "this bank has no
    // IFSC" rather than "nobody has typed it yet".
    expect(screen.getByTestId("stmt-account-identity").textContent).toMatch(
      /no account number or IFSC recorded/i,
    );
  });

  it("shows both when they ARE recorded", async () => {
    const { fire } = renderPanel();
    expect(await screen.findByTestId("brs-as-on")).toBeTruthy();
    await fire();
    expect(await screen.findByTestId("stmt-account-identity")).toBeTruthy();
    const text = screen.getByTestId("stmt-account-identity").textContent ?? "";
    expect(text).toMatch(/50200012345678/);
    expect(text).toMatch(/HDFC0004321/);
  });
});

describe("what the bank said", () => {
  it("labels the columns IN and OUT, so a D does not read as a bank debit", async () => {
    // The bank's own statement calls a deposit a CREDIT — in ITS books we are a
    // liability. Ours stores that deposit as "D". Labelling these columns Dr/Cr
    // would therefore look wrong to every operator holding the paper statement,
    // so the panel says In / Out and puts a "D" under In.
    const { fire } = renderPanel({
      importedStatement: async () =>
        imported({
          lines: [
            {
              id: "sl-1",
              import_id: "imp-1",
              line_no: 2,
              value_date: "2026-04-15",
              txn_date: "",
              description: "NEFT ACME",
              amount: "100000.0000",
              dr_cr: "D",
              bank_reference: "N0421",
              cheque_number: "",
            },
          ],
          total_in: "100000.0000",
        }),
    });
    expect(await screen.findByTestId("brs-as-on")).toBeTruthy();
    await fire();
    await waitFor(() => screen.getByTestId("stmt-show-imported"));
    fireEvent.click(screen.getByTestId("stmt-show-imported"));

    expect(await screen.findByTestId("stmt-lines")).toBeTruthy();
    const table = screen.getByTestId("stmt-lines");
    expect(table.textContent).toMatch(/In/);
    expect(table.textContent).toMatch(/Out/);
    // Indian grouping, from formatMoney, and NOT parsed into a number anywhere.
    expect(table.textContent).toMatch(/1,00,000\.00/);
  });

  it("always carries the report's disclosure that matching is not built", async () => {
    const { fire } = renderPanel();
    expect(await screen.findByTestId("brs-as-on")).toBeTruthy();
    await fire();
    await waitFor(() => screen.getByTestId("stmt-show-imported"));
    fireEvent.click(screen.getByTestId("stmt-show-imported"));

    expect(await screen.findByTestId("stmt-report-disclosures")).toBeTruthy();
    expect(
      screen.getByTestId("stmt-report-disclosures").textContent,
    ).toMatch(/THIS REPORT SHOWS NO MATCH STATUS/);
  });

  it("distinguishes 'nothing imported' from a broken panel", async () => {
    const { fire } = renderPanel();
    expect(await screen.findByTestId("brs-as-on")).toBeTruthy();
    await fire();
    await waitFor(() => screen.getByTestId("stmt-show-imported"));
    fireEvent.click(screen.getByTestId("stmt-show-imported"));
    expect(await screen.findByTestId("stmt-none")).toBeTruthy();
  });
});

describe("refusals reach the operator", () => {
  it("shows the server's named refusal rather than inventing one", async () => {
    const { fire } = renderPanel({
      importBankStatement: async () => {
        throw new Error(
          "bankstatement: the running balances do not walk: at row 3 the balance should be 237499.50 but the file says 212499.50 — the statement is NOT imported",
        );
      },
    });
    expect(await screen.findByTestId("brs-as-on")).toBeTruthy();
    await fire();
    await waitFor(() => screen.getByTestId("stmt-file"));
    chooseFile(CSV);
    await waitFor(() => screen.getByTestId("stmt-import"));
    fireEvent.click(screen.getByTestId("stmt-import"));

    expect(await screen.findByTestId("stmt-error")).toBeTruthy();
    // The ROW NUMBER survives to the screen. A refusal the operator cannot act
    // on is a support call.
    expect(screen.getByTestId("stmt-error").textContent).toMatch(/row 3/);
    expect(screen.getByTestId("stmt-error").textContent).toMatch(
      /NOT imported/,
    );
  });
});

/**
 * ⛔⛔ D111 — A PASTED STATEMENT IS STILL A NAMED STATEMENT.
 *
 * Measured 2026-08-31: two statements pasted into this panel were both stored
 * with `file_name = ''` (confirmed in psql), and the combined "everything
 * imported for this account" report then rendered its per-file disclosure as
 *
 *	· "" carried no running balance, so its rows were NOT balance-walked: …
 *
 * — a sentence whose whole job is to name WHICH file, naming neither of them.
 *
 * ⛔ THE FALLBACK EXISTED AND WAS APPLIED TO ONE OF THE TWO CALLS THAT NEEDED
 * IT, ninety-nine lines apart in this component: the PREVIEW defaulted an empty
 * name to "pasted.csv" and the IMPORT sent the bare empty one. Copying the
 * preview's constant across would have made every paste indistinguishable from
 * every other paste — the same defect one step over — so the name is derived by
 * the SERVER from the statement's own period, row count and fingerprint, for
 * every client rather than for this one.
 */
describe("D111 — the panel shows the name the statement was stored under", () => {
  it("⛔ names a PASTED statement, and says the name was made for it", async () => {
    // ⚠️ THE PARAMETERS ARE TYPED so the assertion below can read the REQUEST
    // the panel sent. A bare `vi.fn(async () => …)` types `mock.calls` as an
    // empty tuple and the one line that pins the client's half of D111 cannot
    // compile.
    const importBankStatement = vi.fn(
      async (
        _companyID: string,
        _ledgerID: string,
        _req: ImportStatementRequest,
      ) =>
        importResult({
          file_name: "pasted statement 2026-04-15 to 2026-04-18, 2 rows (3f9a1c7e)",
        }),
    );
    const { fire } = renderPanel({ importBankStatement });
    expect(await screen.findByTestId("brs-as-on")).toBeTruthy();
    await fire();
    await waitFor(() => screen.getByTestId("stmt-paste"));

    // PASTE, do not choose a file — this is the path that had no name.
    fireEvent.change(screen.getByTestId("stmt-paste"), {
      target: { value: CSV },
    });
    fireEvent.blur(screen.getByTestId("stmt-paste"));
    await waitFor(() => screen.getByTestId("stmt-import"));
    fireEvent.click(screen.getByTestId("stmt-import"));

    const stored = await screen.findByTestId("stmt-stored-name");
    expect(stored.textContent).toContain(
      "pasted statement 2026-04-15 to 2026-04-18, 2 rows (3f9a1c7e)",
    );
    // And it says WHY it has that name, so nobody hunts for a file called that.
    expect(stored.textContent).toMatch(/pasted, so it had no file name/);

    // ⛔ THE PANEL SENDS NO INVENTED NAME. This is the line the defect lived on:
    // an empty name reaching the server is what lets the server derive one, and
    // a client-side "pasted.csv" would have overridden it with a constant.
    expect(importBankStatement.mock.calls[0]![2]!.file_name).toBe("");
  });

  it("⭐ CONTROL: a CHOSEN file keeps its own name and is not explained away", async () => {
    const { fire } = renderPanel();
    expect(await screen.findByTestId("brs-as-on")).toBeTruthy();
    await fire();
    await waitFor(() => screen.getByTestId("stmt-file"));
    chooseFile(CSV);
    await waitFor(() => screen.getByTestId("stmt-import"));
    fireEvent.click(screen.getByTestId("stmt-import"));

    const stored = await screen.findByTestId("stmt-stored-name");
    expect(stored.textContent).toContain("HDFC-april.csv");
    // The "we made this name up" sentence must NOT appear on a real file name.
    expect(stored.textContent).not.toMatch(/pasted, so it had no file name/);
  });
});

/**
 * ⛔ THE MOCK CLIENT MUST NOT AGREE WITH THE BUG. A mock that stores pasted
 * statements nameless lets a screen's handling of D111 pass here and fail
 * against the real server — which is this repository's own stated reason for
 * keeping the mock's refusals in the same order as cored's.
 */
describe("D111 — the mock client names pastes too, and distinguishes them", () => {
  const CSV_A =
    "Value Date,Narration,Withdrawal Amt.,Deposit Amt.\n" +
    '15/04/2026,NEFT ACME,,"1,00,000.00"\n';
  const CSV_B =
    "Value Date,Narration,Withdrawal Amt.,Deposit Amt.\n" +
    '15/04/2026,NEFT BORAX,,"2,00,000.00"\n';
  const MAPPING = {
    encoding: "two-columns" as const,
    date_layout: "dd/mm/yyyy",
    value_date: "Value Date",
    txn_date: "",
    description: "Narration",
    deposit: "Deposit Amt.",
    withdrawal: "Withdrawal Amt.",
    amount: "",
    direction: "",
    running_balance: "",
    bank_reference: "",
    cheque_number: "",
  };

  it("⛔⛔ two different pastes on one account get two different names", async () => {
    const client = createMockClient();
    const { ledgers } = await client.listLedgers("company-demo");
    const bank = ledgers.find((l) => l.is_bank)!;

    const a = await client.importBankStatement("company-demo", bank.id, {
      file_name: "",
      content: CSV_A,
      mapping: MAPPING,
      created_by: "ui",
    });
    const b = await client.importBankStatement("company-demo", bank.id, {
      file_name: "",
      content: CSV_B,
      mapping: MAPPING,
      created_by: "ui",
    });

    expect(a.file_name).toMatch(/^pasted statement 2026-04-15, 1 row \(/);
    expect(b.file_name).toMatch(/^pasted statement 2026-04-15, 1 row \(/);
    expect(a.file_name).not.toBe(b.file_name);

    // ⛔ AND THE SECOND ONE WAS ACTUALLY STORED. Deduplicating on the name the
    // CALLER sent meant every paste matched every other paste — both "" — so
    // the second came back "already imported" having stored nothing.
    expect(b.already_imported).toBe(false);
    const both = await client.importedStatement("company-demo", bank.id);
    expect(both.imports.map((i) => i.file_name).sort()).toEqual(
      [a.file_name, b.file_name].sort(),
    );
  });

  it("⭐ CONTROL: the SAME paste twice is still one import", async () => {
    const client = createMockClient();
    const { ledgers } = await client.listLedgers("company-demo");
    const bank = ledgers.find((l) => l.is_bank)!;
    const req = {
      file_name: "",
      content: CSV_A,
      mapping: MAPPING,
      created_by: "ui",
    };
    const first = await client.importBankStatement("company-demo", bank.id, req);
    const again = await client.importBankStatement("company-demo", bank.id, req);
    expect(again.already_imported).toBe(true);
    expect(again.file_name).toBe(first.file_name);
  });
});
