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
});
