import { describe, it, expect } from "vitest";
import { createActor } from "xstate";
import {
  navMachine,
  projectSnapshot,
  COMPANY_SWITCHED_SENTINEL,
  USERS_ROLES_LABEL,
} from "./navMachine";
import type { NavSnapshot } from "./navMachine";
import { resolveAction, resolveKeymap, actionLabel } from "./keymaps/tallyprime";

/**
 * F3 Switch Company + Users & Roles — the navigation half of the SaaS surface.
 *
 * The load-bearing assertion here is NOT that F3 opens a picker; it is what
 * happens to the STACK when a company is chosen. Every frame below the picker
 * was rendered against the OLD company's books (a half-entered voucher, a drill
 * into last month's Day Book). Leaving them in place and silently re-pointing
 * them at a different tenant is how an entry lands in the wrong company, so the
 * machine unwinds to the Gateway.
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

describe("F3 Switch Company", () => {
  it("is a REAL action, not an 'unavailable' placeholder", () => {
    const keymap = resolveKeymap("tallyprime");
    const action = resolveAction(keymap, "gateway", "F3");
    expect(action?.kind).toBe("switchCompany");
    expect(actionLabel(action!)).toContain("Company");
  });

  it("opens the company picker from the Gateway", () => {
    const snap = drive([{ type: "KEY", combo: "F3" }]);
    expect(snap.screen).toBe("company-select");
    expect(snap.depth).toBe(2);
  });

  it("does not stack a second picker when pressed twice", () => {
    const snap = drive([
      { type: "KEY", combo: "F3" },
      { type: "KEY", combo: "F3" },
    ]);
    expect(snap.screen).toBe("company-select");
    expect(snap.depth).toBe(2);
  });

  it("Esc leaves the picker exactly where the user was", () => {
    const snap = drive([
      { type: "KEY", combo: "F8" }, // a half-entered Sales voucher
      { type: "KEY", combo: "F3" },
      { type: "KEY", combo: "Esc" },
    ]);
    expect(snap.screen).toBe("voucher");
    expect(snap.voucherType).toBe("sales");
  });

  it("switching UNWINDS to the Gateway, discarding frames from the old company", () => {
    // Deep stack against company A: Sales voucher -> F3 picker.
    const snap = drive([
      { type: "SELECT", menuItem: "Day Book" },
      { type: "KEY", combo: "Enter" },
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "F3" },
      { type: "SELECT", menuItem: COMPANY_SWITCHED_SENTINEL },
    ]);
    expect(snap.screen).toBe("gateway");
    expect(snap.depth).toBe(1);
  });

  it("an unrecognised selection in the picker does nothing", () => {
    const snap = drive([
      { type: "KEY", combo: "F3" },
      { type: "SELECT", menuItem: "Some Company · Owner" },
    ]);
    // The SCREEN re-scopes the session and then fires the sentinel; a raw label
    // must not move the machine on its own, or a highlight would look like a
    // switch.
    expect(snap.screen).toBe("company-select");
    expect(snap.depth).toBe(2);
  });
});

describe("Gateway > Users & Roles", () => {
  it("highlights, then drills into the members screen on Enter", () => {
    const highlighted = drive([{ type: "SELECT", menuItem: USERS_ROLES_LABEL }]);
    expect(highlighted.screen).toBe("gateway");
    expect(highlighted.selectedItem).toBe(USERS_ROLES_LABEL);

    const drilled = drive([
      { type: "SELECT", menuItem: USERS_ROLES_LABEL },
      { type: "KEY", combo: "Enter" },
    ]);
    expect(drilled.screen).toBe("members");
  });

  it("is NOT mistaken for a report name", () => {
    // The branch order matters: canonicalReportName() falls back to the raw
    // label, so without a dedicated branch this would push a report frame
    // called "Users & Roles" and render the unwired-report screen.
    const drilled = drive([
      { type: "SELECT", menuItem: USERS_ROLES_LABEL },
      { type: "KEY", combo: "Enter" },
    ]);
    expect(drilled.reportName).toBeUndefined();
  });

  it("Esc returns to the Gateway", () => {
    const snap = drive([
      { type: "SELECT", menuItem: USERS_ROLES_LABEL },
      { type: "KEY", combo: "Enter" },
      { type: "KEY", combo: "Esc" },
    ]);
    expect(snap.screen).toBe("gateway");
  });
});
