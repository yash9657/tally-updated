import { describe, it, expect, afterEach, vi } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";

import { DrillRows, type DrillRow } from "./DrillRows";

/**
 * THE DRILL TARGET — the row the machine believes is highlighted.
 *
 * ⛔ WHAT THESE TESTS ARE ABOUT, AND WHY IT IS NOT A COSMETIC BUG. The shell's
 * Enter does not look at this component at all: it reduces the frame's
 * `selectedItem`, which is whatever `onSelect` last published. So the moment the
 * published key and the highlighted row disagree, Enter on the Day Book opens a
 * DIFFERENT VOUCHER than the one under the cursor — and Ctrl+P on the frame that
 * push produces then prints, or refuses, the wrong document. There is nothing on
 * either screen that says so; both are real vouchers, correctly rendered.
 *
 * The publish used to be an effect keyed on the highlighted key ALONE, which
 * left three ways for the two to drift, all of them reachable from the keyboard:
 *
 *   1. the row list SHRANK under a stale index (F2 narrows the period, Ctrl+F
 *      filters, F12 hides zero rows) — the indexed row was then `undefined`, so
 *      NOTHING was published and the machine kept the previous view's voucher;
 *   2. the list changed to one carrying the SAME key at the cursor — from the
 *      effect's point of view nothing had changed, so nothing was published,
 *      while the frame underneath was new and its selection empty;
 *   3. the component instance was re-used under a freshly pushed frame, which is
 *      what React does for two frames of the same kind in the same position.
 *
 * The fix is two-part and both parts are asserted here: the cursor is CLAMPED to
 * the rows that exist, and Enter RE-PUBLISHES the highlighted row on its way to
 * the shell.
 */

afterEach(cleanup);

function rowsFor(...keys: string[]): DrillRow[] {
  return keys.map((k) => ({ rowKey: k, cells: <span>{k}</span>, values: [k] }));
}

/** The row the operator can SEE highlighted, read off the rendered grid. */
function highlighted(): string | null {
  const el = document
    .querySelector('[role="row"][aria-selected="true"]')
    ?.getAttribute("data-rowkey");
  return el ?? null;
}

function renderRows(rows: DrillRow[], onSelect: (k: string) => void) {
  return render(
    <DrillRows
      rows={rows}
      onSelect={onSelect}
      ariaLabel="Day Book rows"
      columnsClassName="grid-cols-2"
    />,
  );
}

describe("the published row is the highlighted row", () => {
  it("publishes the first drillable row on open", () => {
    const onSelect = vi.fn();
    renderRows(rowsFor("voucher:sales:v1", "voucher:receipt:v2"), onSelect);
    expect(onSelect).toHaveBeenCalledWith("voucher:sales:v1");
    expect(highlighted()).toBe("voucher:sales:v1");
  });

  it("publishes the row the arrows moved to", () => {
    const onSelect = vi.fn();
    renderRows(rowsFor("v1", "v2", "v3"), onSelect);
    const grid = screen.getByRole("grid");
    fireEvent.keyDown(grid, { key: "ArrowDown" });
    fireEvent.keyDown(grid, { key: "ArrowDown" });
    expect(highlighted()).toBe("v3");
    expect(onSelect).toHaveBeenLastCalledWith("v3");
  });

  it("a SHORTER report never leaves the machine holding a row that is gone", () => {
    // ⛔ THE F2 PATH. Arrow down the Day Book, then narrow the period: the row
    // the cursor was on is not in the new list at all. Before the clamp,
    // `drillable[active]` was undefined, the publish was skipped, and the
    // machine went on holding a voucher from the WIDER period — so Enter opened
    // it and Ctrl+P printed it, from a screen that no longer listed it.
    const onSelect = vi.fn();
    const { rerender } = renderRows(rowsFor("v1", "v2", "v3", "v4"), onSelect);
    const grid = screen.getByRole("grid");
    fireEvent.keyDown(grid, { key: "ArrowDown" });
    fireEvent.keyDown(grid, { key: "ArrowDown" });
    fireEvent.keyDown(grid, { key: "ArrowDown" });
    expect(onSelect).toHaveBeenLastCalledWith("v4");

    onSelect.mockClear();
    rerender(
      <DrillRows
        rows={rowsFor("n1")}
        onSelect={onSelect}
        ariaLabel="Day Book rows"
        columnsClassName="grid-cols-2"
      />,
    );
    // The highlight is on the only row there is, and that is what was published.
    expect(highlighted()).toBe("n1");
    expect(onSelect).toHaveBeenCalledWith("n1");
    expect(onSelect).not.toHaveBeenCalledWith("v4");
  });

  it("Arrow Up from a stale cursor lands inside the new report, not near its old index", () => {
    const onSelect = vi.fn();
    const { rerender } = renderRows(rowsFor("a1", "a2", "a3", "a4", "a5"), onSelect);
    const grid = screen.getByRole("grid");
    for (let i = 0; i < 4; i += 1) fireEvent.keyDown(grid, { key: "ArrowDown" });
    rerender(
      <DrillRows
        rows={rowsFor("b1", "b2", "b3")}
        onSelect={onSelect}
        ariaLabel="Day Book rows"
        columnsClassName="grid-cols-2"
      />,
    );
    fireEvent.keyDown(grid, { key: "ArrowUp" });
    // From the clamped position (b3), not from the stored index 4.
    expect(highlighted()).toBe("b2");
    expect(onSelect).toHaveBeenLastCalledWith("b2");
  });
});

describe("Enter publishes the row it drills, whatever the frame's history", () => {
  it("re-publishes the highlighted row even when nothing about the list changed", () => {
    // ⛔ THE RE-OPEN PATH. The same component instance can be re-used under a
    // freshly pushed frame, and a re-opened report can hand back exactly the same
    // rows — so the effect has nothing to react to while the NEW frame's
    // selection is empty. Enter must not depend on that effect having run.
    const onSelect = vi.fn();
    const rows = rowsFor("voucher:sales:v1", "voucher:receipt:v2");
    const { rerender } = renderRows(rows, onSelect);
    const grid = screen.getByRole("grid");
    fireEvent.keyDown(grid, { key: "ArrowDown" });

    onSelect.mockClear();
    // A re-render with an identical row set: the machine's new frame knows
    // nothing, and no effect will fire.
    rerender(
      <DrillRows
        rows={rowsFor("voucher:sales:v1", "voucher:receipt:v2")}
        onSelect={onSelect}
        ariaLabel="Day Book rows"
        columnsClassName="grid-cols-2"
      />,
    );
    expect(onSelect).not.toHaveBeenCalled();

    fireEvent.keyDown(grid, { key: "Enter" });
    expect(onSelect).toHaveBeenCalledWith("voucher:receipt:v2");
    expect(highlighted()).toBe("voucher:receipt:v2");
  });

  it("does NOT swallow Enter — the shell still receives it, and receives it LAST", () => {
    // The drill itself is the shell's document-level handler. This component
    // must publish BEFORE that runs (the machine processes events synchronously,
    // so the frame's selection is already right when Enter is reduced) and must
    // never stop the key from getting there.
    const order: string[] = [];
    const onSelect = vi.fn(() => order.push("select"));
    const shell = () => order.push("shell-enter");
    document.addEventListener("keydown", shell);
    try {
      renderRows(rowsFor("v1", "v2"), onSelect);
      order.length = 0;
      fireEvent.keyDown(screen.getByRole("grid"), { key: "Enter" });
      expect(order).toEqual(["select", "shell-enter"]);
    } finally {
      document.removeEventListener("keydown", shell);
    }
  });

  it("an empty report publishes nothing on Enter rather than an empty key", () => {
    // The machine's own rule: on an empty Day Book, Enter is a no-op. Publishing
    // "" here would push an alteration frame with no voucher behind it.
    const onSelect = vi.fn();
    renderRows([{ rowKey: "total", cells: <span>Total</span>, drillable: false }], onSelect);
    fireEvent.keyDown(screen.getByRole("grid"), { key: "Enter" });
    expect(onSelect).not.toHaveBeenCalled();
  });
});
