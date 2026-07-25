import { describe, it, expect } from "vitest";
import { createActor } from "xstate";
import { navMachine, projectSnapshot, keyChangesStack } from "./navMachine";
import type { NavSnapshot } from "./navMachine";
import { buttonBarLabels } from "./buttonBar";
import {
  resolveAction,
  resolveKeymap,
  keyCatalogFor,
  actionLabel,
} from "./keymaps/tallyprime";
import { safeEvaluate } from "../screens/overlays/FrameOverlays";

/**
 * The BUTTON-FIDELITY gate: a Tally operator navigates the whole product from
 * the keyboard, so this suite asserts that
 *
 *   1. every key the button bar ADVERTISES is bound and does something on the
 *      screen that advertises it (no decorative buttons),
 *   2. every documented TallyPrime combo is bound to SOMETHING — a real action
 *      or an explicit "not built" answer — so no key is silently dead,
 *   3. the newly added navigation semantics behave the way Tally's do
 *      (Esc closes an overlay before it pops a screen, Ctrl+Q quits, Tab walks
 *      the field cursor, F12/F2/Ctrl+F/Alt+F1/F5 act on reports, …).
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

/** Would this combo change the stack, given the events that came before it? */
function acts(events: Ev[], combo: string): boolean {
  const actor = createActor(navMachine, {
    input: { startScreen: "gateway", keymapProfile: "tallyprime" },
  });
  actor.start();
  for (const ev of events) actor.send(ev);
  const changed = keyChangesStack(actor.getSnapshot().context, combo);
  actor.stop();
  return changed;
}

const keymap = resolveKeymap("tallyprime");

/** The screens whose button bars we assert, with the events that reach them. */
const SCREEN_PATHS: { screen: NavSnapshot["screen"]; events: Ev[] }[] = [
  { screen: "gateway", events: [] },
  { screen: "voucher", events: [{ type: "KEY", combo: "F8" }] },
  {
    screen: "report",
    events: [
      { type: "SELECT", menuItem: "Day Book" },
      { type: "KEY", combo: "Enter" },
    ],
  },
  {
    screen: "master-create",
    events: [
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "Alt+C" },
    ],
  },
];

describe("no advertised button is decorative", () => {
  for (const { screen, events } of SCREEN_PATHS) {
    it(`every button on the ${screen} bar is bound and acts there`, () => {
      const snap = drive(events);
      expect(snap.screen).toBe(screen);
      for (const label of buttonBarLabels(screen, snap.voucherType)) {
        const combo = label.split(" ")[0] as string;
        const action = resolveAction(keymap, screen, combo);
        // (1) bound at all…
        expect(action, `${combo} on ${screen} is unbound`).toBeTruthy();
        // (2) …and it is not an "unavailable" placeholder on a bar…
        expect(
          action?.kind,
          `${combo} is advertised on the ${screen} bar but is not built`,
        ).not.toBe("unavailable");
        // (3) …and it either moves the machine, is a screen-owned side effect
        // (accept/delete/export leave the stack alone), or is one of the keys
        // that is meaningful even when the resulting frame is identical — F8
        // inside a Sales voucher re-opens a FRESH Sales voucher, which is a
        // byte-identical frame but very much an action.
        const sideEffect = [
          "accept",
          "delete",
          "exportScreen",
          "voucher",
          "date",
        ].includes(action?.kind ?? "");
        expect(
          sideEffect || acts(events, combo),
          `${combo} does nothing on ${screen}`,
        ).toBe(true);
      }
    });
  }
});

describe("no documented TallyPrime key is silently dead", () => {
  const DOCUMENTED = [
    // RESEARCH.md sec 4 — function keys and their combinations.
    "F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12",
    "Alt+F1", "Alt+F2", "Alt+F3", "Alt+F5", "Alt+F6", "Alt+F7", "Alt+F8", "Alt+F9",
    "Ctrl+F1", "Ctrl+F3", "Ctrl+F4", "Ctrl+F7", "Ctrl+F8", "Ctrl+F9",
    // Navigation and accept keys.
    "Enter", "Esc", "Ctrl+A", "Ctrl+Q", "Tab", "Shift+Tab", "PageUp", "PageDown",
    "Ctrl+N", "Alt+C", "Alt+D", "Alt+X", "Alt+2", "Alt+G", "Ctrl+G", "Ctrl+H",
    "Alt+E", "Ctrl+F",
  ];

  for (const combo of DOCUMENTED) {
    it(`${combo} is bound`, () => {
      const action = resolveAction(keymap, "gateway", combo);
      expect(action, `${combo} is not bound at all`).toBeTruthy();
      // Every action must have a human label — that is what the status line and
      // the F1 Help screen show, so an unlabelled action would read as blank.
      expect(actionLabel(action!).length).toBeGreaterThan(0);
    });
  }

  it("keys for features this build lacks say so, with a reason", () => {
    for (const combo of ["Alt+F6", "Ctrl+F4", "F10"]) {
      const action = resolveAction(keymap, "gateway", combo);
      expect(action?.kind).toBe("unavailable");
      if (action?.kind === "unavailable") {
        expect(action.feature).toContain(combo);
        expect(action.note.length).toBeGreaterThan(20);
      }
    }
  });

  it("the F1 Help catalog covers every bound key for the screen", () => {
    const rows = keyCatalogFor(keymap, "report");
    const combos = rows.map((r) => r.combo);
    expect(combos).toContain("Ctrl+A");
    expect(combos).toContain("Alt+F6");
    // The report override wins over the global binding in the catalog.
    const f5 = rows.find((r) => r.combo === "F5");
    expect(f5?.action.kind).toBe("ledgerWise");
  });
});

describe("context-scoped keys (Tally re-uses F-keys per screen)", () => {
  it("F5 opens a Payment voucher from the Gateway but toggles ledger-wise in a report", () => {
    expect(resolveAction(keymap, "gateway", "F5")).toEqual({
      kind: "voucher",
      voucherType: "payment",
    });
    expect(resolveAction(keymap, "report", "F5")).toEqual({ kind: "ledgerWise" });
  });

  it("F2 is the voucher DATE but the report PERIOD", () => {
    expect(resolveAction(keymap, "voucher", "F2")?.kind).toBe("date");
    expect(resolveAction(keymap, "report", "F2")?.kind).toBe("period");
  });
});

describe("overlay semantics: Esc cancels the overlay, not the screen", () => {
  const intoReport: Ev[] = [
    { type: "SELECT", menuItem: "Trial Balance" },
    { type: "KEY", combo: "Enter" },
  ];

  it("F12 opens Configure over the report and Esc returns to the SAME report", () => {
    const open = drive([...intoReport, { type: "KEY", combo: "F12" }]);
    expect(open.screen).toBe("report");
    expect(open.overlay).toBe("configure");

    const closed = drive([
      ...intoReport,
      { type: "KEY", combo: "F12" },
      { type: "KEY", combo: "Esc" },
    ]);
    expect(closed.screen).toBe("report");
    expect(closed.reportName).toBe("Trial Balance");
    expect(closed.overlay).toBeUndefined();
    expect(closed.screenStackDepth).toBe(2);
  });

  it("Ctrl+H in a voucher opens Change Mode; Esc closes the picker, not the voucher", () => {
    const snap = drive([
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "Ctrl+H" },
      { type: "KEY", combo: "Esc" },
    ]);
    expect(snap.screen).toBe("voucher");
    expect(snap.overlay).toBeUndefined();
  });

  it("Ctrl+Q quits the whole screen even with an overlay open", () => {
    const snap = drive([
      ...intoReport,
      { type: "KEY", combo: "Ctrl+F" },
      { type: "KEY", combo: "Ctrl+Q" },
    ]);
    expect(snap.screen).toBe("gateway");
  });

  it("Ctrl+N opens the calculator anywhere and Esc closes it", () => {
    const open = drive([{ type: "KEY", combo: "Ctrl+N" }]);
    expect(open.overlay).toBe("calculator");
    const closed = drive([
      { type: "KEY", combo: "Ctrl+N" },
      { type: "KEY", combo: "Esc" },
    ]);
    expect(closed.overlay).toBeUndefined();
    expect(closed.screen).toBe("gateway");
  });
});

describe("report view keys mutate the frame's view config", () => {
  const intoReport: Ev[] = [
    { type: "SELECT", menuItem: "Balance Sheet" },
    { type: "KEY", combo: "Enter" },
  ];

  it("Alt+F1 toggles Detailed", () => {
    expect(drive([...intoReport, { type: "KEY", combo: "Alt+F1" }]).config?.detailed).toBe(true);
    expect(
      drive([
        ...intoReport,
        { type: "KEY", combo: "Alt+F1" },
        { type: "KEY", combo: "Alt+F1" },
      ]).config?.detailed,
    ).toBe(false);
  });

  it("Ctrl+H changes the report VIEW (it is Change Mode only in vouchers)", () => {
    const snap = drive([...intoReport, { type: "KEY", combo: "Ctrl+H" }]);
    expect(snap.config?.vertical).toBe(true);
    expect(snap.overlay).toBeUndefined();
  });

  it("F5 flips ledger-wise off (the report opens ledger-wise)", () => {
    expect(drive([...intoReport, { type: "KEY", combo: "F5" }]).config?.ledgerWise).toBe(false);
  });

  it("view keys do NOT apply inside a voucher (and the shell can tell)", () => {
    expect(acts([{ type: "KEY", combo: "F8" }], "Alt+F1")).toBe(false);
    expect(acts([{ type: "KEY", combo: "F8" }], "Ctrl+F")).toBe(false);
  });
});

describe("field cursor: Tab / Shift+Tab", () => {
  it("Tab advances and Shift+Tab steps back, clamped at the first field", () => {
    const one = drive([
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "Tab" },
    ]);
    expect(one.field).toBe("party-ledger");

    const back = drive([
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "Tab" },
      { type: "KEY", combo: "Shift+Tab" },
    ]);
    expect(back.field).toBe("date");

    const clamped = drive([
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "Shift+Tab" },
    ]);
    expect(clamped.field).toBe("date");
  });
});

describe("F1 Help and F11 Features are real screens", () => {
  it("F1 pushes Help over the current screen and Esc returns to it", () => {
    const help = drive([
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "F1" },
    ]);
    expect(help.screen).toBe("help");
    expect(help.parentScreen).toBe("voucher");

    const back = drive([
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "F1" },
      { type: "KEY", combo: "Esc" },
    ]);
    expect(back.screen).toBe("voucher");
  });

  it("F1 pressed twice does not stack two Help screens", () => {
    const snap = drive([
      { type: "KEY", combo: "F1" },
      { type: "KEY", combo: "F1" },
    ]);
    expect(snap.screen).toBe("help");
    expect(snap.screenStackDepth).toBe(2);
  });

  it("F11 opens Company Features", () => {
    expect(drive([{ type: "KEY", combo: "F11" }]).screen).toBe("features");
  });
});

describe("Alt+X cancel and Alt+2 duplicate", () => {
  it("Alt+X resets an unsaved voucher frame", () => {
    const snap = drive([
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "Enter" },
      { type: "KEY", combo: "Enter" },
      { type: "KEY", combo: "Alt+X" },
    ]);
    expect(snap.screen).toBe("voucher");
    expect(snap.field).toBe("date");
  });

  it("Alt+2 turns an alteration into a fresh create (so Ctrl+A posts a NEW voucher)", () => {
    const snap = drive([
      { type: "SELECT", menuItem: "Day Book" },
      { type: "KEY", combo: "Enter" }, // report
      { type: "KEY", combo: "Enter" }, // drill to the voucher (alter)
      { type: "KEY", combo: "Alt+2" },
    ]);
    expect(snap.screen).toBe("voucher");
    expect(snap.mode).toBe("create");
  });
});

describe("Ctrl+N calculator arithmetic (no eval, fixed-4)", () => {
  it("evaluates precedence and parentheses", () => {
    expect(safeEvaluate("2+3*4")).toBe("14.0000");
    expect(safeEvaluate("(2+3)*4")).toBe("20.0000");
    expect(safeEvaluate("118000/1.18")).toBe("100000.0000");
    expect(safeEvaluate("-5+2")).toBe("-3.0000");
  });

  it("rejects anything that is not arithmetic", () => {
    expect(safeEvaluate("alert(1)")).toBe("invalid expression");
    expect(safeEvaluate("2+")).toBe("invalid expression");
    expect(safeEvaluate("1/0")).toBe("divide by zero");
  });
});
