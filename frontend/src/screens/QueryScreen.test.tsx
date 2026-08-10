import { describe, it, expect, afterEach, vi } from "vitest";
import {
  render,
  screen,
  cleanup,
  waitFor,
  fireEvent,
  act,
} from "@testing-library/react";
import { QueryScreen } from "./QueryScreen";
import { CompanyProvider } from "../app/CompanyContext";
import { ApiError } from "../api";
import type { CoredClient, QueryResult } from "../api";
import type { NavSnapshot } from "../keystroke/navMachine";

afterEach(cleanup);

const SNAP: NavSnapshot = {
  screen: "query",
  screenStackDepth: 2,
  canReturnToVoucher: false,
};

/** A canned QueryResult: a two-column, two-row answer with money-as-string. */
const CANNED: QueryResult = {
  dsl: {
    entity: "vouchers",
    metrics: [{ func: "sum", column: "taxable_value", as: "total_sales" }],
    dimensions: ["month"],
    filters: [
      { column: "voucher_type_name", op: "eq", values: ["Sales"] },
    ],
    date_range: { column: "voucher_date", from: "2025-04-01", to: "2026-03-31" },
    order_by: [{ column: "month", desc: false }],
    limit: 100,
  },
  sql_preview:
    "SELECT to_char(vv.voucher_date,'YYYY-MM'), COALESCE(SUM(vv.taxable_value),0) FROM ... WHERE vt.name = $1 ...",
  columns: [
    { name: "month", type: "text" },
    { name: "total_sales", type: "numeric" },
  ],
  rows: [
    ["2025-04", "15000.0000"],
    ["2025-05", "22500.5000"],
  ],
};

function renderQuery(client: Partial<CoredClient>) {
  return render(
    <CompanyProvider
      client={client as CoredClient}
      initialCompany={{ id: "co-1", name: "Test Co" }}
    >
      <QueryScreen snapshot={SNAP} onSelect={() => {}} />
    </CompanyProvider>,
  );
}

describe("QueryScreen (Ask your books)", () => {
  it("submits the typed NL question to client.query and renders the columns + rows verbatim", async () => {
    const query = vi.fn(async () => CANNED);
    renderQuery({ query });

    const input = screen.getByTestId("query-input") as HTMLInputElement;
    await act(async () => {
      fireEvent.change(input, { target: { value: "sales this month" } });
      fireEvent.submit(input.closest("form")!);
    });

    // The NL string was sent as { nl } to the active company (read-only path).
    await waitFor(() =>
      expect(query).toHaveBeenCalledWith("co-1", { nl: "sales this month" }),
    );

    // Column headers render.
    await waitFor(() => expect(screen.getByText("total_sales")).toBeTruthy());
    expect(screen.getByText("month")).toBeTruthy();

    // Money is rendered as the server STRING, verbatim (no parseFloat coercion).
    expect(screen.getByText("15000.0000")).toBeTruthy();
    expect(screen.getByText("22500.5000")).toBeTruthy();
    expect(screen.getByText("2025-04")).toBeTruthy();
    expect(screen.getAllByTestId("query-row")).toHaveLength(2);
  });

  it("clicking an example chip runs that question", async () => {
    const query = vi.fn(async () => CANNED);
    renderQuery({ query });

    await act(async () => {
      fireEvent.click(screen.getByTestId("query-example-trial balance"));
    });

    await waitFor(() =>
      expect(query).toHaveBeenCalledWith("co-1", { nl: "trial balance" }),
    );
  });

  it("reveals the sql_preview and the DSL in the 'how this was answered' panel", async () => {
    const query = vi.fn(async () => CANNED);
    renderQuery({ query });

    const input = screen.getByTestId("query-input") as HTMLInputElement;
    await act(async () => {
      fireEvent.change(input, { target: { value: "sales this month" } });
      fireEvent.submit(input.closest("form")!);
    });
    await waitFor(() => expect(screen.getByTestId("query-table")).toBeTruthy());

    // The panel is collapsed by default; toggling reveals the preview + DSL.
    expect(screen.queryByTestId("query-how")).toBeNull();
    await act(async () => {
      fireEvent.click(screen.getByTestId("query-how-toggle"));
    });
    const preview = screen.getByTestId("query-sql-preview");
    // The preview carries $N placeholders, never an interpolated user value.
    expect(preview.textContent).toContain("$1");
    expect(preview.textContent).not.toContain("Sales");
    // The echoed DSL is shown.
    expect(screen.getByTestId("query-dsl").textContent).toContain("total_sales");
  });

  it("on a 422 shows the helpful message and the example questions", async () => {
    const query = vi.fn(async () => {
      throw new ApiError(422, "could not understand the question", {
        error: "could not understand the question",
        examples: ["trial balance", "sales this month", "top debtors"],
      });
    });
    renderQuery({ query });

    const input = screen.getByTestId("query-input") as HTMLInputElement;
    await act(async () => {
      fireEvent.change(input, { target: { value: "asdf qwerty" } });
      fireEvent.submit(input.closest("form")!);
    });

    await waitFor(() =>
      expect(screen.getByTestId("query-error").textContent).toContain(
        "could not understand the question",
      ),
    );
    // The server-provided examples are surfaced so the operator can rephrase.
    const examples = screen.getAllByTestId("query-error-example");
    expect(examples.map((e) => e.textContent)).toEqual([
      "trial balance",
      "sales this month",
      "top debtors",
    ]);
    // No result table is shown on an error.
    expect(screen.queryByTestId("query-table")).toBeNull();
  });

  // ------------------------------------------------------------------
  // DISCLOSURE (D21 / D50 / D51). These assert the two captions that make a
  // plausible-looking answer legible. See QueryScreen's DISCLOSURE note.
  // ------------------------------------------------------------------

  it("states the company scope BEFORE any question, on an answer, and on a refusal (D50)", async () => {
    // A cross-tenant probe is REFUSED server-side (query.ErrCrossTenantScope ->
    // 422). What this asserts is the half that cannot drift: the scope caption
    // is on screen in all three states, so a question naming a company by a
    // name nobody enumerated is still answered under a visible "this company
    // only" statement rather than silently.
    const query = vi
      .fn()
      .mockResolvedValueOnce(CANNED)
      .mockRejectedValueOnce(
        new ApiError(422, "could not understand the question", {
          error: "could not understand the question",
          examples: ["trial balance"],
        }),
      );
    renderQuery({ query });

    const scope = () => screen.getByTestId("query-scope").textContent ?? "";
    // (1) before anything has been asked.
    expect(scope()).toContain("Test Co");
    expect(scope()).toContain("cannot read another company");

    // (2) with an answer on screen.
    const input = screen.getByTestId("query-input") as HTMLInputElement;
    await act(async () => {
      fireEvent.change(input, { target: { value: "sales this month" } });
      fireEvent.submit(input.closest("form")!);
    });
    await waitFor(() => expect(screen.getByTestId("query-table")).toBeTruthy());
    expect(scope()).toContain("Test Co");

    // (3) with a refusal on screen — the state a cross-tenant probe lands in.
    await act(async () => {
      fireEvent.change(input, {
        target: { value: "receivables for all companies" },
      });
      fireEvent.submit(input.closest("form")!);
    });
    await waitFor(() => expect(screen.getByTestId("query-error")).toBeTruthy());
    expect(scope()).toContain("Test Co");
    expect(scope()).toContain("cannot read another company");
  });

  it("prints the ANSWERED PERIOD above the table, without opening any panel (D21)", async () => {
    const query = vi.fn(async () => CANNED);
    renderQuery({ query });

    const input = screen.getByTestId("query-input") as HTMLInputElement;
    await act(async () => {
      fireEvent.change(input, { target: { value: "sales last quarter" } });
      fireEvent.submit(input.closest("form")!);
    });
    await waitFor(() => expect(screen.getByTestId("query-table")).toBeTruthy());

    // The collapsed "how this was answered" panel is STILL collapsed — the
    // window must be legible without it, which is exactly what D21 lacked.
    expect(screen.queryByTestId("query-how")).toBeNull();
    const period = screen.getByTestId("query-basis-period").textContent ?? "";
    expect(period).toContain(CANNED.dsl.date_range!.from);
    expect(period).toContain(CANNED.dsl.date_range!.to);
  });

  it("says ALL DATES when the answer carries no date window (D21)", async () => {
    const noWindow: QueryResult = {
      ...CANNED,
      dsl: { ...CANNED.dsl, date_range: null },
    };
    const query = vi.fn(async () => noWindow);
    renderQuery({ query });

    const input = screen.getByTestId("query-input") as HTMLInputElement;
    await act(async () => {
      fireEvent.change(input, { target: { value: "total sales" } });
      fireEvent.submit(input.closest("form")!);
    });
    await waitFor(() => expect(screen.getByTestId("query-table")).toBeTruthy());
    expect(screen.getByTestId("query-basis-period").textContent).toContain(
      "ALL DATES",
    );
  });

  it("discloses WHAT WAS MEASURED from the echoed DSL, not from the question (D51)", async () => {
    // The server answers "total sales" with sum(natural_net) over the Sales
    // Accounts P&L root. The caption must say so — that it is the P&L's own
    // figure, net of credit notes and exclusive of GST — because the driven
    // defect was a number that matched no report and said nothing about itself.
    const netSales: QueryResult = {
      ...CANNED,
      dsl: {
        ...CANNED.dsl,
        entity: "postings",
        metrics: [{ func: "sum", column: "natural_net", as: "net_sales" }],
        filters: [
          { column: "pl_group", op: "eq", values: ["Sales Accounts"] },
        ],
      },
    };
    const query = vi.fn(async () => netSales);
    renderQuery({ query });

    const input = screen.getByTestId("query-input") as HTMLInputElement;
    await act(async () => {
      fireEvent.change(input, { target: { value: "total sales" } });
      fireEvent.submit(input.closest("form")!);
    });
    await waitFor(() => expect(screen.getByTestId("query-table")).toBeTruthy());

    const measure = screen.getByTestId("query-basis-measure").textContent ?? "";
    expect(measure).toContain("Profit & Loss");
    expect(measure).toContain("credit/debit notes");
    expect(measure).toContain("EXCLUDES GST");
  });

  it("discloses the outstandings basis, including the as-at-today bound (D20)", async () => {
    const pending: QueryResult = {
      ...CANNED,
      dsl: {
        ...CANNED.dsl,
        entity: "outstandings",
        metrics: [{ func: "sum", column: "pending", as: "pending" }],
        dimensions: ["ledger_name"],
        filters: [{ column: "nature", op: "eq", values: ["ASSET"] }],
        date_range: null,
      },
      columns: [
        { name: "ledger_name", type: "text" },
        { name: "pending", type: "numeric" },
      ],
      rows: [["Acme Corp", "128800.0000"]],
    };
    const query = vi.fn(async () => pending);
    renderQuery({ query });

    const input = screen.getByTestId("query-input") as HTMLInputElement;
    await act(async () => {
      fireEvent.change(input, { target: { value: "top debtors" } });
      fireEvent.submit(input.closest("form")!);
    });
    await waitFor(() => expect(screen.getByTestId("query-table")).toBeTruthy());

    const measure = screen.getByTestId("query-basis-measure").textContent ?? "";
    // The opening bill-wise breakup is the source D20 records as missing; the
    // caption names it so a reader can tell which figure they are looking at.
    expect(measure).toContain("opening");
    expect(measure).toContain("Outstandings report");
    expect(measure).toContain("AS AT TODAY");
  });

  it("never leaves a figure undescribed: an unknown metric column still gets a basis line", async () => {
    // The disclosure must have NO silent fallthrough. A measure the caption
    // does not recognise is restated literally rather than omitted — an
    // undisclosed number is the defect, whatever produced it.
    const odd: QueryResult = {
      ...CANNED,
      dsl: {
        ...CANNED.dsl,
        metrics: [{ func: "max", column: "some_future_column", as: "m" }],
      },
    };
    const query = vi.fn(async () => odd);
    renderQuery({ query });

    const input = screen.getByTestId("query-input") as HTMLInputElement;
    await act(async () => {
      fireEvent.change(input, { target: { value: "anything" } });
      fireEvent.submit(input.closest("form")!);
    });
    await waitFor(() => expect(screen.getByTestId("query-table")).toBeTruthy());
    expect(screen.getByTestId("query-basis-measure").textContent).toContain(
      "max(some_future_column)",
    );
  });

  it("on a 503 shows a retry hint, NOT the example-question list", async () => {
    const query = vi.fn(async () => {
      throw new ApiError(503, "translator unavailable; retry or POST a typed dsl", {
        error: "translator unavailable; retry or POST a typed dsl",
      });
    });
    renderQuery({ query });

    const input = screen.getByTestId("query-input") as HTMLInputElement;
    await act(async () => {
      fireEvent.change(input, { target: { value: "how were sales last spring" } });
      fireEvent.submit(input.closest("form")!);
    });

    // The distinct retry hint is shown…
    await waitFor(() =>
      expect(screen.getByTestId("query-retry-hint")).toBeTruthy(),
    );
    expect(screen.getByTestId("query-error").textContent).toContain(
      "translator unavailable",
    );
    // …and the canned examples are NOT (a 503 is an operator fault, not a bad
    // question — offering "try one of these" would be misleading).
    expect(screen.queryByTestId("query-error-example")).toBeNull();
    expect(screen.queryByTestId("query-table")).toBeNull();
  });
});
