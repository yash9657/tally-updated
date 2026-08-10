import { describe, it, expect, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";

import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient } from "../api/mock";

/**
 * ⛔⛔ THE KEYBOARD CAN REACH THE FORM (D38).
 *
 * The cost-allocation picker was built, wired, tested and **unreachable**: on the
 * particulars grid Tab and Shift+Tab moved nothing, so `alloc:0-add`, the axis and
 * centre selects, the amount box, "rest", "×" and "+ split" were MOUSE-ONLY — on a
 * product whose whole claim is that a Tally operator never touches the mouse, and
 * whose hint strip on that very screen advertises "Tab / Shift+Tab: Field".
 *
 * The cause was one clause in `useNavMachine`'s `onKeyDown`: it called
 * `preventDefault()` for Tab and Shift+Tab, and the machine's replacement for the
 * browser's focus walk was a FIVE-STRING HIGHLIGHT that never touches
 * `document.activeElement`. Tab is bound GLOBALLY in the keymap, so the loss was
 * not the voucher's — it was the product's.
 *
 * ===========================================================================
 * ⚠️ WHAT THIS FILE CAN AND CANNOT OBSERVE, AND WHY THE ORACLE IS WHAT IT IS
 * ===========================================================================
 * jsdom does NOT implement sequential focus navigation: a real Chromium moves
 * focus on Tab, jsdom does nothing at all, no matter what the app does. So
 * "focus moved" is not directly assertable here and a test that claimed to
 * observe it would be observing its own helper.
 *
 * The oracle is therefore split into the two facts that TOGETHER are the fix,
 * each of which IS observable:
 *
 *   1. **The app leaves the key alone.** `fireEvent` returns false when
 *      `preventDefault` was called, so "Tab is not defaultPrevented" is a
 *      direct, executable assertion about the exact line that was the defect.
 *   2. **The controls are in the tab order.** Enumerated from the REAL rendered
 *      grid — not a fixture — so a control that a future change hides behind
 *      `tabindex="-1"`, `disabled` or a non-focusable element fails here.
 *
 * {@link walkTab} then composes the two the way a browser does: dispatch the
 * key, and move focus ONLY if nothing prevented the default. Point 1 is what
 * makes point 2 reachable, which is exactly the shape of the defect.
 */

const COMPANY = "company-demo";

afterEach(cleanup);

function renderShell() {
  render(
    <CompanyProvider
      client={createMockClient()}
      initialCompany={{ id: COMPANY, name: "Demo Traders" }}
    >
      <AppShell />
    </CompanyProvider>,
  );
  return screen.getByRole("application");
}

/**
 * Every control the browser's sequential focus navigation would visit, in
 * document order.
 *
 * ⛔ THE `:not()` FILTERS ARE THE POINT, not tidiness. A `disabled` select and a
 * `tabindex="-1"` button are both rendered, both clickable-looking and both
 * UNREACHABLE from the keyboard — which is the exact class of defect this file
 * exists to catch. Excluding them here is what makes {@link tabOrderIds} an
 * honest statement about what a keyboard user can get to.
 */
function focusables(root: HTMLElement): HTMLElement[] {
  return [
    ...root.querySelectorAll<HTMLElement>(
      [
        "input:not([disabled])",
        "select:not([disabled])",
        "button:not([disabled])",
        "textarea:not([disabled])",
        "a[href]",
        '[tabindex]:not([tabindex="-1"])',
      ].join(","),
    ),
  ].filter((el) => el.getAttribute("tabindex") !== "-1");
}

/** The `data-testid`s (or aria-labels) of the tab order, for readable assertions. */
function tabOrderIds(root: HTMLElement): string[] {
  return focusables(root).map(
    (el) =>
      el.getAttribute("data-testid") ??
      el.getAttribute("aria-label") ??
      el.tagName.toLowerCase(),
  );
}

/**
 * One Tab press, the way a browser performs it: deliver the key to the app
 * FIRST, and only move focus if the app did not prevent the default.
 *
 * Returns the element focus ends on, or null when the app swallowed the key —
 * which is precisely what the defect did, on every screen, for every control.
 */
function walkTab(
  root: HTMLElement,
  opts: { shift?: boolean } = {},
): HTMLElement | null {
  const order = focusables(root);
  const here = order.indexOf(document.activeElement as HTMLElement);
  const notPrevented = fireEvent.keyDown(document.activeElement ?? document, {
    key: "Tab",
    code: "Tab",
    shiftKey: opts.shift === true,
    bubbles: true,
  });
  if (!notPrevented) return null; // the app ate it: the browser does nothing.
  const next = opts.shift === true ? here - 1 : here + 1;
  const target = order[next];
  if (!target) return null;
  target.focus();
  return target;
}

/** F5 Payment in single entry, with one particular keyed. */
async function openPaymentWithAParticular(app: HTMLElement, amount = "1000") {
  fireEvent.keyDown(app, { key: "F5" });
  await screen.findByTestId("single-entry-panel");
  fireEvent.change(screen.getByLabelText("Particular 1 ledger"), {
    target: { value: "Electricity" },
  });
  fireEvent.change(screen.getByLabelText("Particular 1 amount"), {
    target: { value: amount },
  });
}

describe("⛔ Tab is the browser's, not the machine's", () => {
  it("does NOT preventDefault Tab or Shift+Tab — the focus walk is handed back", () => {
    const app = renderShell();
    // fireEvent returns false when preventDefault was called. Both directions,
    // because Shift+Tab was in the same swallowed clause and a half fix here is
    // an operator who can go forwards and never back.
    expect(
      fireEvent.keyDown(app, { key: "Tab", code: "Tab" }),
      "Tab was defaultPrevented — the browser's focus walk is dead and the machine substitutes nothing for it",
    ).toBe(true);
    expect(
      fireEvent.keyDown(app, { key: "Tab", code: "Tab", shiftKey: true }),
      "Shift+Tab was defaultPrevented",
    ).toBe(true);
  });

  it("STILL swallows the chords that would otherwise hit the browser", () => {
    // The other half of the rule: a key whose handler DOES replace the browser's
    // behaviour must keep preventing it. Ctrl+A accepts the voucher and must not
    // also Select All; F9 opens a Purchase and must not open the browser's menu.
    // Without this, "stop preventing Tab" is one careless edit away from
    // "stop preventing anything".
    const app = renderShell();
    expect(
      fireEvent.keyDown(app, { key: "a", code: "KeyA", ctrlKey: true }),
      "Ctrl+A must stay prevented or the browser Select-All fires alongside the accept",
    ).toBe(false);
    expect(
      fireEvent.keyDown(app, { key: "F9", code: "F9" }),
      "F9 must stay prevented",
    ).toBe(false);
  });

  it("Tab still advances the machine's field cursor (the corpus pins it)", () => {
    // corpora/keystroke/voucher-tab-walks-fields.json. Handing the focus walk
    // back must not delete the highlight: the two now run side by side, and the
    // highlight is decoration while the focus ring is the truth.
    const app = renderShell();
    fireEvent.keyDown(app, { key: "F5" });
    const before = screen.getByTestId("voucher-date").className;
    fireEvent.keyDown(app, { key: "Tab", code: "Tab" });
    expect(screen.getByTestId("voucher-date").className).not.toBe(before);
  });
});

describe("⛔ every control of the cost-allocation picker is in the tab order", () => {
  it("reaches the 'allocate' button from the particular's amount box", async () => {
    const app = renderShell();
    await openPaymentWithAParticular(app);

    const amount = screen.getByTestId("single-amount:0");
    amount.focus();
    expect(document.activeElement).toBe(amount);

    // Forward: amount -> remove? (absent with one row) -> the allocate button.
    const visited: string[] = [];
    for (let i = 0; i < 4; i++) {
      const el = walkTab(app);
      if (!el) break;
      visited.push(el.getAttribute("data-testid") ?? "");
    }
    expect(
      visited,
      "Tab from the amount box never reached alloc:0-add — the cost split is mouse-only",
    ).toContain("alloc:0-add");
  });

  it("enumerates the WHOLE row once a split exists, and each control is reachable", async () => {
    const app = renderShell();
    await openPaymentWithAParticular(app, "1000");

    // Reach the add button by keyboard, then activate it. In a real browser a
    // focused <button> is activated by Enter or Space (a browser default this app
    // does not prevent — see the Enter assertion below); jsdom implements no
    // activation behaviour for a key, so the click is fired directly and the
    // KEY's survival is asserted separately.
    const add = await screen.findByTestId("alloc:0-add");
    add.focus();
    expect(
      fireEvent.keyDown(add, { key: "Enter", code: "Enter" }),
      "Enter was defaultPrevented on a focused button, so the keyboard cannot activate it",
    ).toBe(true);
    fireEvent.click(add);

    const order = tabOrderIds(app);
    // ⛔ THE ENUMERATION, not a spot check. Each of these is a control the
    // operator must be able to key; a regression that drops any one of them
    // re-ships the defect for that control alone.
    for (const id of [
      "single-ledger:0",
      "single-amount:0",
      "alloc:0-axis:0",
      "alloc:0-amount:0",
      "alloc:0-remove:0",
      "alloc:0-more",
      "single-add-line",
      "voucher-narration",
    ]) {
      expect(order, `${id} is not reachable by Tab`).toContain(id);
    }

    // The CENTRE select is deliberately absent until an axis is chosen: it is
    // `disabled`, and a disabled control is correctly out of the tab order. That
    // is a designed gate, not an unreachable control — choosing the axis is what
    // opens it, and the very next assertion proves the gate lifts.
    expect(order).not.toContain("alloc:0-centre:0");
    const axis = screen.getByTestId("alloc:0-axis:0") as HTMLSelectElement;
    fireEvent.change(axis, { target: { value: axis.options[1]!.value } });
    const after = tabOrderIds(app);
    expect(
      after,
      "picking an axis must put the centre select into the tab order",
    ).toContain("alloc:0-centre:0");
    // And "rest" only exists once an axis is chosen, for the same reason.
    expect(after).toContain("alloc:0-rest:0");
  });

  it("Shift+Tab walks back out of the picker", async () => {
    const app = renderShell();
    await openPaymentWithAParticular(app, "1000");
    fireEvent.click(await screen.findByTestId("alloc:0-add"));

    const split = screen.getByTestId("alloc:0-amount:0");
    split.focus();
    const back = walkTab(app, { shift: true });
    expect(
      back,
      "Shift+Tab moved nothing — an operator who overshoots a split is stuck",
    ).not.toBeNull();
    expect(back!.getAttribute("data-testid")).toBe("alloc:0-axis:0");
  });
});
