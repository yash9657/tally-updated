import { describe, it, expect } from "vitest";
import { createActor } from "xstate";
import {
  navMachine,
  projectSnapshot,
  GOTO_ENTRIES,
  MASTER_GOTO_ENTRIES,
  masterKindForGotoLabel,
} from "./navMachine";
import type { NavSnapshot } from "./navMachine";
import { REPORT_NAMES } from "./types";
import type { MasterKind } from "./types";

/**
 * ⛔⛔ AUDIT ROW 20: A COMPANY COULD NOT BE GIVEN A STOCK ITEM OR A UNIT FROM THE
 * KEYBOARD AT ALL.
 *
 * Driven in a browser 2026-08-07: `Alt+C` rendered "Create ledger" from the
 * Gateway AND from the item-invoice Item cell, the Gateway menu carried no
 * masters entry, and the Alt+G picker offered `["Vouchers", ...REPORT_NAMES]`.
 * So of the FIVE `MasterKind`s the type union declares, exactly ONE was
 * reachable, and `MasterCreateScreen`'s other four arms were dead code that
 * looked alive because every test called the component directly.
 *
 * ⚠️ THIS FILE'S FIRST TEST IS THE ONE THAT MATTERS, and it is written as an
 * EXHAUSTIVE TABLE over `MasterKind` rather than as a list of cases: a sixth
 * kind added to the union makes `REACHED_BY` fail to compile, so a kind cannot be
 * declared without a keystroke that reaches it. A list of `it(...)` blocks would
 * have stayed green through exactly the defect this file exists for.
 */

type Ev =
  | { type: "KEY"; combo: string }
  | { type: "SELECT"; menuItem: string };

function drive(events: Ev[]): NavSnapshot {
  const actor = createActor(navMachine, {
    input: { startScreen: "gateway", keymapProfile: "tallyprime" },
  });
  actor.start();
  for (const ev of events) actor.send(ev);
  const snap = projectSnapshot(actor.getSnapshot().context);
  actor.stop();
  return snap;
}

/**
 * EVERY `MasterKind` and the keystroke sequence that reaches it — including the
 * one that already worked, because "the reported call site is not the class".
 *
 * `Record<MasterKind, …>` is deliberate: TypeScript refuses this object if a kind
 * is missing, so the union and this table cannot drift apart.
 */
const REACHED_BY: Record<MasterKind, { how: string; events: Ev[] }> = {
  ledger: {
    // The door that always existed, kept under test so a Go To fix cannot
    // silently break the inline one.
    how: "Alt+C from a voucher field (inline, on the fly)",
    events: [
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "Alt+C" },
    ],
  },
  group: {
    how: "Alt+G > Create Group",
    events: [
      { type: "KEY", combo: "Alt+G" },
      { type: "SELECT", menuItem: "Create Group" },
      { type: "KEY", combo: "Enter" },
    ],
  },
  "voucher-type": {
    how: "Alt+G > Create Voucher Type",
    events: [
      { type: "KEY", combo: "Alt+G" },
      { type: "SELECT", menuItem: "Create Voucher Type" },
      { type: "KEY", combo: "Enter" },
    ],
  },
  unit: {
    how: "Alt+G > Create Unit",
    events: [
      { type: "KEY", combo: "Alt+G" },
      { type: "SELECT", menuItem: "Create Unit" },
      { type: "KEY", combo: "Enter" },
    ],
  },
  "stock-item": {
    how: "Alt+G > Create Stock Item",
    events: [
      { type: "KEY", combo: "Alt+G" },
      { type: "SELECT", menuItem: "Create Stock Item" },
      { type: "KEY", combo: "Enter" },
    ],
  },
};

describe("⛔ every MasterKind is reachable from the keyboard (audit row 20)", () => {
  it("each of the five kinds has a keystroke that opens its create frame", () => {
    for (const [kind, route] of Object.entries(REACHED_BY) as [
      MasterKind,
      { how: string; events: Ev[] },
    ][]) {
      const snap = drive(route.events);
      expect(
        snap.screen,
        `${kind} should be reachable by ${route.how}`,
      ).toBe("master-create");
      expect(snap.masterKind, `${kind} via ${route.how}`).toBe(kind);
      // The cursor must land ON the name; a create frame with nothing focused
      // is a dead screen in a keystroke product.
      expect(snap.field).toBe("name");
    }
  });

  it("⛔ CONTROL: a master entry is NOT resolved as a report name", () => {
    // `canonicalReportName` falls back to the RAW label for anything it does not
    // recognise, so an entry reaching that line would push
    // `{screen:"report", reportName:"Create Stock Item"}` — the UnwiredReport
    // screen politely announcing that the thing you asked to create "is not
    // available in this build". That is the failure this ordering prevents.
    for (const label of Object.keys(MASTER_GOTO_ENTRIES)) {
      const snap = drive([
        { type: "KEY", combo: "Alt+G" },
        { type: "SELECT", menuItem: label },
        { type: "KEY", combo: "Enter" },
      ]);
      expect(snap.screen, label).not.toBe("report");
      expect(snap.reportName, label).toBeUndefined();
    }
  });

  it("⛔ CONTROL: Go To still opens the reports it always did", () => {
    for (const name of REPORT_NAMES) {
      const snap = drive([
        { type: "KEY", combo: "Alt+G" },
        { type: "SELECT", menuItem: name },
        { type: "KEY", combo: "Enter" },
      ]);
      expect(snap.screen, name).toBe("report");
      expect(snap.reportName, name).toBe(name);
    }
  });

  it("matches the label case-insensitively, as every Gateway label is", () => {
    const snap = drive([
      { type: "KEY", combo: "Alt+G" },
      { type: "SELECT", menuItem: "  create STOCK item  " },
      { type: "KEY", combo: "Enter" },
    ]);
    expect(snap.screen).toBe("master-create");
    expect(snap.masterKind).toBe("stock-item");
    expect(masterKindForGotoLabel("CREATE UNIT")).toBe("unit");
    expect(masterKindForGotoLabel("Balance Sheet")).toBeUndefined();
    expect(masterKindForGotoLabel(undefined)).toBeUndefined();
  });

  it("Ctrl+G (Switch To) reaches the same frames and marks them non-returnable", () => {
    const snap = drive([
      { type: "KEY", combo: "F8" }, // a voucher underneath, to be replaced
      { type: "KEY", combo: "Ctrl+G" },
      { type: "SELECT", menuItem: "Create Unit" },
      { type: "KEY", combo: "Enter" },
    ]);
    expect(snap.screen).toBe("master-create");
    expect(snap.masterKind).toBe("unit");
    // Switch To destroys the context it came from — Esc cannot return to it.
    expect(snap.canReturnToVoucher).toBe(false);
    expect(snap.parentScreen).toBe("gateway");
  });

  /**
   * ⛔ THE PICKER AND THE MACHINE MUST AGREE, and this is the assertion that
   * makes RULE 3 ("never ship an unreachable fix") structural rather than a
   * promise: every entry the picker RENDERS must resolve to something the
   * machine ROUTES, and every master label the machine routes must be offered.
   */
  it("GOTO_ENTRIES offers exactly what the machine can route", () => {
    for (const label of Object.keys(MASTER_GOTO_ENTRIES)) {
      expect(GOTO_ENTRIES).toContain(label);
    }
    for (const name of REPORT_NAMES) expect(GOTO_ENTRIES).toContain(name);
    expect(GOTO_ENTRIES).toContain("Vouchers");
    // Nothing in the list is a dead end: it is either Vouchers, a master entry
    // or a report name.
    for (const entry of GOTO_ENTRIES) {
      const routed =
        entry === "Vouchers" ||
        masterKindForGotoLabel(entry) !== undefined ||
        (REPORT_NAMES as readonly string[]).includes(entry);
      expect(routed, `${entry} is offered but routed nowhere`).toBe(true);
    }
  });
});

describe("Ctrl+A after a STANDALONE create returns to where it came from", () => {
  it("pops to the Gateway and leaves NO pending selection on it", () => {
    // ⛔ THE REGRESSION THIS GUARDS. `selectedItem` on the Gateway is the row
    // Enter drills into. Stamping the created master's name there made the very
    // next Enter push `{screen:"report", reportName:"Acme Traders"}` — a report
    // that does not exist — so accepting a ledger left the operator one keypress
    // from a broken screen. Only a VOUCHER parent takes the pending pick.
    const snap = drive([
      { type: "KEY", combo: "Alt+G" },
      { type: "SELECT", menuItem: "Create Ledger" },
      { type: "KEY", combo: "Enter" },
      { type: "SELECT", menuItem: "Acme Traders" },
      { type: "KEY", combo: "Ctrl+A" },
    ]);
    expect(snap.screen).toBe("gateway");
    expect(snap.screenStackDepth).toBe(1);
    expect(snap.selectedItem).toBeUndefined();
  });

  it("⛔ CONTROL: the INLINE Alt+C flow still pre-fills the voucher field", () => {
    const snap = drive([
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "Alt+C" },
      { type: "SELECT", menuItem: "Acme Traders" },
      { type: "KEY", combo: "Ctrl+A" },
    ]);
    expect(snap.screen).toBe("voucher");
    expect(snap.selectedItem).toBe("Acme Traders");
  });

  it("Esc abandons a standalone create without creating anything", () => {
    const snap = drive([
      { type: "KEY", combo: "Alt+G" },
      { type: "SELECT", menuItem: "Create Stock Item" },
      { type: "KEY", combo: "Enter" },
      { type: "KEY", combo: "Esc" },
    ]);
    expect(snap.screen).toBe("gateway");
  });
});
