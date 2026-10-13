import { describe, it, expect, afterEach, vi } from "vitest";
import { useState } from "react";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";

import { CompanyProvider } from "../../app/CompanyContext";
import type { CoredClient, LedgerListItem } from "../../api";
import { DoubleEntryPanel } from "./DoubleEntryPanel";
import { emptyDoubleEntryLine, type DoubleEntryLine } from "./doubleEntry";

/**
 * DoubleEntryPanel.forex.test.tsx — ⭐⭐ T8 STEP 8's ENTRY FIELD.
 *
 * ===========================================================================
 * ⛔⛔ THE TWO ASSERTIONS THIS FILE EXISTS FOR, NEITHER OF WHICH IS A LAYOUT
 * ===========================================================================
 *  1. THAT THE FIELDS ARE ABSENT ON A COMPANY WITH NO CURRENCY. That is every
 *     company in the field today. Three permanently blank boxes on every line of
 *     the form an operator uses most is noise, and noise on a form is how a real
 *     field stops being read.
 *  2. THAT THE "= fgn × rate" SUGGESTION USES THE **RATE** PARSER AND NOT THE
 *     MONEY ONE. `parseFixed4` truncates the fifth decimal onward — its own
 *     comment says so — so computing the base from 83.6512 instead of
 *     83.6512345678 gives a figure that is plausible, in the right box, and wrong
 *     for ever. It is the §6.3 failure moved onto the entry form, where it would
 *     be written into the books rather than merely displayed.
 *
 * ⚠️ AND THE THIRD, WHICH IS ABOUT UNDOING: clearing the currency must clear all
 * THREE values. Migration 0080's CHECK refuses a two-of-three row, so a row left
 * with a foreign amount and no currency is a 422 on a request that looks complete
 * on this side — and leaving an operator to clear two more boxes to undo one
 * choice is exactly how that row gets sent.
 */

afterEach(cleanup);

const USD = { id: "cur-usd", code: "USD" };
const EUR = { id: "cur-eur", code: "EUR" };

function Harness({
  currencies,
  initial,
  onLines,
}: {
  currencies?: readonly { id: string; code: string }[];
  initial?: DoubleEntryLine[];
  onLines?: (lines: readonly DoubleEntryLine[]) => void;
}) {
  function Inner() {
    const [lines, setLines] = useState<DoubleEntryLine[]>(
      initial ?? [emptyDoubleEntryLine()],
    );
    return (
      <DoubleEntryPanel
        voucherClass="journal"
        lines={lines}
        setLines={(next) =>
          setLines((prev) => {
            const out = next(prev);
            onLines?.(out);
            return out;
          })
        }
        parties={["Globex LLC", "Export Sales"]}
        ledgerByName={() => undefined as LedgerListItem | undefined}
        kindForGroup={() => "RECEIVABLE"}
        currencies={currencies}
      />
    );
  }
  return (
    <CompanyProvider
      // ⚠️ THE COST PICKER ON EVERY ROW READS THE CHART TOO. It is stubbed rather
      // than left out: an undefined method throws inside a child component and
      // the whole grid fails to render, which would make every assertion below
      // fail for a reason that has nothing to do with currency.
      client={
        {
          getPartyOpenBills: vi.fn(async () => ({
            kind: "RECEIVABLE",
            ledger_id: "",
            ledger: "",
            group: "",
            pending: "0.0000",
            found: false,
            bills: [],
          })),
          // ⛔⛔ BOTH KEYS FROM THIS ONE CALL. `useCostMasters` reads
          // `res.cost_categories` AND `res.cost_centres` off the SAME
          // `listCostCentres` response (the axes are served with the centres so a
          // create form never asks for an id) — so a stub returning only
          // `cost_centres` sets `axes` to undefined and `axes.length` throws
          // during render, seven times, once per row rendered in this file.
          //
          // ⚠️ AND `as unknown as CoredClient` IS WHY THE COMPILER DID NOT SAY SO.
          // The cast is needed to stub three methods of a large interface, and it
          // switches off the check that would have caught a missing key. The
          // earlier `listCostCategories` stub beside it was never called at all —
          // there is no second request to make.
          listCostCentres: vi.fn(async () => ({
            cost_centres: [],
            cost_categories: [],
          })),
        } as unknown as CoredClient
      }
      initialCompany={{ id: "co-1", name: "Test Co", homeStateCode: "29" }}
    >
      <Inner />
    </CompanyProvider>
  );
}

describe("T8/8: the currency fields appear only when the company has a currency", () => {
  it("⭐ CONTROL — a company with NO currency sees the grid exactly as before", () => {
    render(<Harness />);
    // The grid itself is there…
    expect(screen.getByTestId("double-entry-panel")).toBeTruthy();
    expect(screen.getByTestId("de-amount:0")).toBeTruthy();
    // …and none of step 8's boxes are.
    expect(screen.queryByTestId("de-currency:0")).toBeNull();
    expect(screen.queryByTestId("de-foreign:0")).toBeNull();
    expect(screen.queryByTestId("de-rate:0")).toBeNull();
  });

  it("offers the picker once a currency exists, and never the company's OWN", () => {
    render(<Harness currencies={[USD, EUR]} />);
    const options = Array.from(
      screen.getByTestId("de-currency:0").querySelectorAll("option"),
    ).map((o) => o.textContent ?? "");
    // ⛔ THE EMPTY OPTION IS "the company's own", and it is not a currency row.
    // 0080's trigger refuses a master for the base currency, because a base
    // amount is recorded with NO currency at all — so "own" cannot be a row in
    // this list and must be the absence of one.
    //
    // ⚠️ IN THE ORDER GIVEN, NOT SORTED HERE. `VoucherScreen` sorts the list by
    // code before passing it; the panel renders what it is handed. Sorting in
    // both places is how two orderings of one list start disagreeing, and
    // sorting only here would leave the sort out of reach of the test that
    // actually cares which order an operator sees.
    expect(options).toEqual(["₹ own", "USD", "EUR"]);
  });

  it("⛔ hides the foreign amount and rate until a currency is chosen", () => {
    render(<Harness currencies={[USD]} />);
    // The currency picker is there and the other two are not: a foreign amount
    // box with no currency chosen invites exactly the two-of-three row 0080's
    // CHECK refuses.
    expect(screen.getByTestId("de-currency:0")).toBeTruthy();
    expect(screen.queryByTestId("de-foreign:0")).toBeNull();
    expect(screen.queryByTestId("de-rate:0")).toBeNull();

    fireEvent.change(screen.getByTestId("de-currency:0"), {
      target: { value: "cur-usd" },
    });
    expect(screen.getByTestId("de-foreign:0")).toBeTruthy();
    expect(screen.getByTestId("de-rate:0")).toBeTruthy();
  });
});

describe("T8/8: clearing the currency clears ALL THREE values", () => {
  it("⛔ leaves no two-of-three row behind", () => {
    const seeded: DoubleEntryLine[] = [
      {
        ...emptyDoubleEntryLine(),
        ledger: "Globex LLC",
        amount: "83651.23",
        currencyId: "cur-usd",
        currencyCode: "USD",
        foreignAmount: "1000.00",
        rate: "83.6512345678",
      },
    ];
    const onLines = vi.fn();
    render(<Harness currencies={[USD]} initial={seeded} onLines={onLines} />);
    fireEvent.change(screen.getByTestId("de-currency:0"), {
      target: { value: "" },
    });
    const after = onLines.mock.calls.at(-1)?.[0] as DoubleEntryLine[];
    expect(after[0]?.currencyId).toBeUndefined();
    expect(after[0]?.foreignAmount).toBeUndefined();
    expect(after[0]?.rate).toBeUndefined();
    // ⭐ AND THE BASE AMOUNT IS UNTOUCHED. It is the authoritative figure (Q2);
    // clearing a currency is not a reason to throw away what the operator keyed
    // off the invoice in front of them.
    expect(after[0]?.amount).toBe("83651.23");
  });
});

describe("T8/8: the '= fgn × rate' suggestion", () => {
  const seeded = (rate: string): DoubleEntryLine[] => [
    {
      ...emptyDoubleEntryLine(),
      ledger: "Globex LLC",
      amount: "",
      currencyId: "cur-usd",
      currencyCode: "USD",
      foreignAmount: "1000.00",
      rate,
    },
  ];

  it("⛔⛔ uses the TEN-DECIMAL rate and not the fixed-4 money parser", () => {
    const onLines = vi.fn();
    render(
      <Harness
        currencies={[USD]}
        initial={seeded("83.6512345678")}
        onLines={onLines}
      />,
    );
    fireEvent.click(screen.getByTestId("de-base-from-forex:0"));
    const after = onLines.mock.calls.at(-1)?.[0] as DoubleEntryLine[];
    // 1000 × 83.6512345678 = 83,651.2345678 -> 83,651.2346 at money scale.
    //
    // ⛔ THE WRONG ANSWER IS 83,651.2000 — what `parseFixed4` on the rate gives
    // (83.6512 × 1000) — and it is plausible, in the right box, and wrong for
    // ever. That is exactly the arithmetic §6.3 of the handoff is about, moved
    // from a report onto the form that WRITES the books.
    expect(after[0]?.amount).toBe("83651.2346");
    expect(after[0]?.amount).not.toBe("83651.2000");
  });

  it("rounds HALF-AWAY-FROM-ZERO, the way the server's decimal does", () => {
    const onLines = vi.fn();
    render(
      <Harness
        currencies={[USD]}
        initial={seeded("83.65125")}
        onLines={onLines}
      />,
    );
    fireEvent.click(screen.getByTestId("de-base-from-forex:0"));
    const after = onLines.mock.calls.at(-1)?.[0] as DoubleEntryLine[];
    // 1000 × 83.65125 = 83,651.25 exactly — no rounding needed, and the point of
    // the case is that an exact half in the PRODUCT does not drift.
    expect(after[0]?.amount).toBe("83651.2500");
  });

  it("⭐ SUGGESTS and never DECIDES — it only fires when pressed", () => {
    const onLines = vi.fn();
    render(
      <Harness
        currencies={[USD]}
        initial={seeded("83.6512345678")}
        onLines={onLines}
      />,
    );
    // ⛔ Q2 MAKES THE BASE AMOUNT AUTHORITATIVE, so nothing may quietly overwrite
    // what the operator keyed. Typing the foreign amount and the rate must leave
    // the base alone; an invoice whose rupee figure is a negotiated number rather
    // than the day's arithmetic is an ordinary invoice, not an error.
    fireEvent.change(screen.getByTestId("de-foreign:0"), {
      target: { value: "1200.00" },
    });
    const after = onLines.mock.calls.at(-1)?.[0] as DoubleEntryLine[];
    expect(after[0]?.amount).toBe("");
  });
});

describe("T8/8: the panel refuses a half-keyed triple before anything is sent", () => {
  it("names which value is missing", () => {
    render(
      <Harness
        currencies={[USD]}
        initial={[
          {
            ...emptyDoubleEntryLine(),
            ledger: "Globex LLC",
            amount: "83651.23",
            currencyId: "cur-usd",
            currencyCode: "USD",
            foreignAmount: "1000.00",
            // ⛔ NO RATE.
          },
          {
            ...emptyDoubleEntryLine(),
            ledger: "Export Sales",
            side: "CR",
            amount: "83651.23",
          },
        ]}
      />,
    );
    // ⛔ THE FORM SAYS IT, NOT THE SERVER. The server and a DB CHECK both refuse
    // this row — but by then `ensureLedger` has created every account the
    // operator named, which is defect D39's shape and costs them the form.
    expect(screen.getByText(/no EXCHANGE RATE/)).toBeTruthy();
  });

  it("⭐ CONTROL — a balanced grid with a COMPLETE triple shows no refusal", () => {
    render(
      <Harness
        currencies={[USD]}
        initial={[
          {
            ...emptyDoubleEntryLine(),
            ledger: "Globex LLC",
            amount: "83651.23",
            currencyId: "cur-usd",
            currencyCode: "USD",
            foreignAmount: "1000.00",
            rate: "83.6512345678",
          },
          {
            ...emptyDoubleEntryLine(),
            ledger: "Export Sales",
            side: "CR",
            amount: "83651.23",
          },
        ]}
      />,
    );
    expect(screen.queryByText(/no EXCHANGE RATE/)).toBeNull();
    expect(screen.queryByText(/no CURRENCY/)).toBeNull();
    // ⭐ AND THE VOUCHER BALANCES IN BASE AND NEED NOT BALANCE IN ANY FOREIGN
    // CURRENCY (Q2). One line carries $1,000 and the other carries none, and
    // that is a correct multi-currency journal.
    expect(screen.queryByText(/difference/i)).toBeNull();
  });
});
