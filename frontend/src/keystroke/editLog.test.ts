import { describe, it, expect } from "vitest";
import { createActor } from "xstate";
import { navMachine, projectSnapshot } from "./navMachine";
import type { NavSnapshot } from "./navMachine";
import { resolveAction, resolveKeymap, actionLabel } from "./keymaps/tallyprime";
import { buttonBarLabels } from "./buttonBar";
import { voucherRowKey } from "./voucherClass";

/**
 * Alt+L — THE EDIT LOG, the navigation half.
 *
 * The interesting assertions are the REFUSALS. Tally surfaces the Edit Log from
 * the voucher alteration screen (RESEARCH.md sec 6), and a voucher being ENTERED
 * has no history at all — pushing an empty log screen for it would read as a
 * broken screen rather than as "there is nothing to show". So the binding is
 * scoped to the voucher screen AND to alteration mode, and this file pins both
 * halves of that.
 */

type Ev =
  | { type: "KEY"; combo: string }
  | { type: "SELECT"; menuItem: string };

function drive(events: Ev[]): NavSnapshot & { depth: number } {
  const actor = createActor(navMachine, {
    input: { startScreen: "gateway", keymapProfile: "tallyprime" },
  });
  actor.start();
  for (const ev of events) actor.send(ev);
  const ctx = actor.getSnapshot().context;
  const snap = projectSnapshot(ctx);
  actor.stop();
  return { ...snap, depth: ctx.stack.length };
}

/**
 * Day Book -> Enter (report) -> highlight a row -> Enter (drill) puts a voucher
 * in ALTER mode.
 *
 * ⚠️ THE ROW SELECT IS NOT DECORATION. The drill reads the CLASS out of the row
 * key, so a row that names none is not drilled at all — which is what an EMPTY
 * Day Book publishes, and pushing an alteration frame for it used to let Ctrl+A
 * POST a brand-new Sales voucher. `DrillRows` publishes the first drillable row
 * on mount, so the live screen always sends this; these tests drive the pure
 * machine, so they have to send it themselves.
 */
const TO_ALTERATION: Ev[] = [
  { type: "SELECT", menuItem: "Day Book" },
  { type: "KEY", combo: "Enter" },
  { type: "SELECT", menuItem: voucherRowKey("v-1", "sales") },
  { type: "KEY", combo: "Enter" },
];

const keymap = resolveKeymap("tallyprime");

describe("Alt+L opens the Edit Log", () => {
  it("is bound on the voucher screen, with a human label", () => {
    const action = resolveAction(keymap, "voucher", "Alt+L");
    expect(action?.kind).toBe("editLog");
    expect(actionLabel(action!)).toContain("Edit Log");
  });

  it("pushes the edit-log screen from a voucher in ALTERATION mode", () => {
    const before = drive(TO_ALTERATION);
    expect(before.screen).toBe("voucher");
    expect(before.mode).toBe("alter");

    const snap = drive([...TO_ALTERATION, { type: "KEY", combo: "Alt+L" }]);
    expect(snap.screen).toBe("edit-log");
    expect(snap.depth).toBe(before.depth + 1);
  });

  it("Esc returns to the voucher it was opened from", () => {
    const snap = drive([
      ...TO_ALTERATION,
      { type: "KEY", combo: "Alt+L" },
      { type: "KEY", combo: "Esc" },
    ]);
    expect(snap.screen).toBe("voucher");
    expect(snap.mode).toBe("alter");
  });

  it("does NOT stack a second log if pressed twice", () => {
    const once = drive([...TO_ALTERATION, { type: "KEY", combo: "Alt+L" }]);
    const twice = drive([
      ...TO_ALTERATION,
      { type: "KEY", combo: "Alt+L" },
      { type: "KEY", combo: "Alt+L" },
    ]);
    expect(twice.screen).toBe("edit-log");
    expect(twice.depth).toBe(once.depth);
  });

  it("does NOTHING on a voucher being ENTERED — it has no history yet", () => {
    const before = drive([{ type: "KEY", combo: "F8" }]);
    const snap = drive([
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "Alt+L" },
    ]);
    expect(before.mode).toBe("create");
    expect(snap.screen).toBe("voucher");
    expect(snap.depth).toBe(before.depth);
  });

  it("does NOTHING outside a voucher", () => {
    const gateway = drive([{ type: "KEY", combo: "Alt+L" }]);
    expect(gateway.screen).toBe("gateway");
    expect(gateway.depth).toBe(1);

    const report = drive([
      { type: "SELECT", menuItem: "Day Book" },
      { type: "KEY", combo: "Enter" },
      { type: "KEY", combo: "Alt+L" },
    ]);
    expect(report.screen).toBe("report");
  });

  it("the edit-log screen advertises a reader button bar", () => {
    // A bar offering voucher-entry keys on an immutable audit log would be
    // decorative at best and misleading at worst.
    const labels = buttonBarLabels("edit-log");
    expect(labels.length).toBeGreaterThan(0);
    expect(labels.join(" ")).not.toMatch(/Accept|Delete/i);
  });
});
