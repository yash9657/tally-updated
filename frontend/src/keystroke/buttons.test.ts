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
  // The Bank Reconciliation Statement, reached by the Gateway entry (TallyPrime's
  // own route is Alt+G > Banking Activities — RESEARCH.md:873).
  {
    screen: "bank-reconciliation",
    events: [
      { type: "SELECT", menuItem: "Bank Reconciliation" },
      { type: "KEY", combo: "Enter" },
    ],
  },
  // The TDS CHALLAN register (T6), reached by its own Gateway entry.
  {
    screen: "tds-challans",
    events: [
      { type: "SELECT", menuItem: "TDS Challans" },
      { type: "KEY", combo: "Enter" },
    ],
  },
  // The three T2 COST screens, each by its own Gateway entry. Reaching them
  // through the Gateway is itself part of what these paths assert: two of the
  // three labels are real report names on the server, so a missing branch in the
  // machine would land on a `report` frame instead and `snap.screen` below would
  // not match.
  {
    screen: "cost-centres",
    events: [
      { type: "SELECT", menuItem: "Cost Centres" },
      { type: "KEY", combo: "Enter" },
    ],
  },
  {
    screen: "cost-centre-summary",
    events: [
      { type: "SELECT", menuItem: "Cost Centre Summary" },
      { type: "KEY", combo: "Enter" },
    ],
  },
  {
    screen: "cost-category-summary",
    events: [
      { type: "SELECT", menuItem: "Cost Category Summary" },
      { type: "KEY", combo: "Enter" },
    ],
  },
  // ---- THE THREE T3 SCREENS.
  //
  // The two REGISTERS are reached BY THEIR REAL KEYS here rather than through the
  // Gateway, and that is deliberate: Ctrl+F8 and Ctrl+F9 are documented TallyPrime
  // combos (RESEARCH.md:400, :614, :616) and the key is the primary door, so driving
  // the key is what proves the whole chain — the global binding, the `orderRegister`
  // action, and the machine case that pushes the frame. If any link were missing,
  // `snap.screen` below would still be "gateway" and the assertion names the screen
  // that did not open.
  //
  // ⛔ AND THE TWO PATHS PROVE THE AUTHORITY SPLIT IS REAL. Ctrl+F8 must reach
  // `sales-orders` and Ctrl+F9 `purchase-orders`; one screen serving both directions
  // would make one of these two lines unsatisfiable.
  { screen: "sales-orders", events: [{ type: "KEY", combo: "Ctrl+F8" }] },
  { screen: "purchase-orders", events: [{ type: "KEY", combo: "Ctrl+F9" }] },
  // The BOM screen's only door is the Gateway: RESEARCH pins no key for reaching a
  // bill of materials, because in Tally it is a page of the stock item's own master
  // (RESEARCH.md:626) rather than a screen, and inventing a combo would be inventing
  // a keystroke.
  {
    screen: "boms",
    events: [
      { type: "SELECT", menuItem: "Bills of Materials" },
      { type: "KEY", combo: "Enter" },
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
          // Print fetches a PDF from the server and hands it to the browser. Like
          // export it deliberately does NOT move the stack: an operator who
          // prints an invoice is still on that invoice.
          "print",
          // F4 on the reconciliation screen picks the bank account
          // (RESEARCH.md:879). The screen owns the picker, because which ledgers
          // are BANK ledgers is an ltree ancestry fact in the database, not
          // something the pure machine can know.
          "bankReconciliation",
          // Alt+O on the reconciliation screen opens the statement-IMPORT panel
          // (T4 slice 2a; RESEARCH.md:361 "O: Import"). A screen-owned side
          // effect for the same reason F4 above is one: the import belongs to
          // the SELECTED bank account, and which account that is never travels
          // through the pure machine. It does not move the stack — importing
          // does not navigate away from the statement you are reconciling.
          "importBankStatement",
          // Alt+C on the TDS challan register opens the DEPOSIT FORM. A
          // screen-owned side effect for the same reason Alt+O is one: the form
          // state and the client that posts it belong to the screen, and
          // recording a deposit does not navigate — so the pure machine
          // deliberately does not move and this rule would otherwise read the
          // bar button as decorative.
          "recordTDSChallan",
          // Alt+C on the cost masters screen opens the CREATE panel, and F4 on
          // the Cost Centre Summary opens the AXIS picker. Both are screen-owned
          // for the same reason as the two above — the panel state, the served
          // list of axes and the client that posts belong to the screen — and
          // neither navigates, so the pure machine deliberately does not move.
          "createCostMaster",
          "selectCostCategory",
          // The four T3 screen actions: Alt+C / Alt+W on the two order registers
          // and Alt+C / Alt+X on the BOM screen. Screen-owned for the same reasons
          // as the two above — the form state, the served pick-lists and the client
          // that posts all belong to the screen — plus two of their own: Alt+W acts
          // on the order HIGHLIGHTED in the list, and Alt+X is a server round-trip
          // against a bom_id the pure machine has never seen. None of the four
          // navigates, so the machine deliberately does not move and this rule
          // would otherwise read all four bar buttons as decorative.
          "createOrder",
          "closeOrder",
          "createBom",
          "explodeBom",
          "voucher",
          // Ctrl+F8 / Ctrl+F9 on the register they already opened. pushOnce leaves
          // the stack byte-identical rather than stacking a duplicate frame, so the
          // key is meaningful without changing anything — see ALWAYS_MEANINGFUL in
          // AppShell.
          "orderRegister",
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

  it("Alt+F5 / Alt+F6 open the REAL Debit / Credit Note screens", () => {
    // These were bound as "unavailable" until the note classes existed. They are
    // the correct way to reverse part of an invoice, so they must reach a real
    // voucher screen — if either regresses to "unavailable", the F11 Features
    // page and the keymap would contradict each other.
    const dn = resolveAction(keymap, "gateway", "Alt+F5");
    expect(dn?.kind).toBe("voucher");
    if (dn?.kind === "voucher") expect(dn.voucherType).toBe("debit-note");
    const cn = resolveAction(keymap, "gateway", "Alt+F6");
    expect(cn?.kind).toBe("voucher");
    if (cn?.kind === "voucher") expect(cn.voucherType).toBe("credit-note");
  });

  it("the stock voucher keys open the REAL non-accounting screens", () => {
    // Alt+F7/F8/F9/F10 were `unavailable` while the four classes had no entry
    // screen. They have one now (StockVoucherScreen), so the keys must reach it
    // — and they are advertised on the voucher button bar, which the rule above
    // permits only because they no longer resolve to `unavailable`.
    const expected: [string, string][] = [
      ["Alt+F7", "stock-journal"],
      ["Alt+F8", "delivery-note"],
      ["Alt+F9", "receipt-note"],
      ["Alt+F10", "physical-stock"],
    ];
    for (const [combo, voucherType] of expected) {
      const action = resolveAction(keymap, "gateway", combo);
      expect(action?.kind, `${combo} must open a voucher screen`).toBe("voucher");
      if (action?.kind === "voucher") expect(action.voucherType).toBe(voucherType);
    }
  });

  it("the order keys open the REAL order registers", () => {
    // Ctrl+F8 / Ctrl+F9 were `unavailable` ("Order processing is not built yet")
    // in two stages and for two different reasons — the same two-stage history
    // the stock voucher keys above have. First the SERVER could not hold a
    // commitment (the stock ledger's one primitive is a quantity plus a
    // direction, and an order is neither); then the tables, both books and all
    // fourteen client methods existed and there was NO SCREEN, so a
    // keyboard-only operator could not create an order at all.
    //
    // ⛔ TWO KEYS AND TWO ACTIONS, NOT ONE WITH A TOGGLE. `sales-order` and
    // `purchase-order` are separate authorities on separate routes and the PDP
    // reads the PATH, so a single screen would be reaching a resource the
    // operator may be denied. This assertion is what stops the two being
    // "simplified" into one.
    const expected: [string, string][] = [
      ["Ctrl+F8", "sales"],
      ["Ctrl+F9", "purchase"],
    ];
    for (const [combo, direction] of expected) {
      const action = resolveAction(keymap, "gateway", combo);
      expect(action?.kind, `${combo} must open an order register`).toBe(
        "orderRegister",
      );
      if (action?.kind === "orderRegister") {
        expect(action.direction).toBe(direction);
      }
      // And it must be LABELLED, because an unlabelled action reads as a blank
      // line in the F1 Help catalog and the status line.
      expect(actionLabel(action!).length).toBeGreaterThan(0);
    }
  });

  it("keys for features this build lacks say so, with a reason", () => {
    // ⚠️ THE EXAMPLES IN THIS LOOP HAVE NOW BEEN REPLACED TWICE, and that is the
    // point of the test rather than a nuisance: Alt+F7 was the original stand-in
    // and the stock vouchers got built, then Ctrl+F8 replaced it and ORDER
    // PROCESSING got built. Each time, this assertion is what forced the keymap's
    // "not built yet" note to be corrected instead of left to rot into a lie that
    // sends an operator looking for a feature they already have.
    //
    // What is left here is genuinely absent: the payroll voucher (Ctrl+F4) has no
    // screen, F10's "Other Vouchers" PICKER does not exist (every voucher it would
    // list has its own working key, which is what its note now says), and shutting
    // a company (Ctrl+F3) is not built.
    for (const combo of ["Ctrl+F4", "F10", "Ctrl+F3"]) {
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
