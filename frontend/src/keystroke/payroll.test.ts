import { describe, it, expect } from "vitest";
import { createActor } from "xstate";
import {
  navMachine,
  projectSnapshot,
  EMPLOYEE_ROW_PREFIX,
  EMPLOYEES_LABEL,
  PAY_RUN_LABEL,
  PAYROLL_REGISTER_LABEL,
  FORM_138_LABEL,
} from "./navMachine";
import { buttonBarFor } from "./buttonBar";
import { resolveAction, resolveKeymap, actionLabel } from "./keymaps/tallyprime";

/**
 * THE PAYROLL NAVIGATION — and the ONE trap that makes it worth its own suite.
 *
 * ⛔ THE PROPERTY: NO PAYROLL DOOR MAY LAND ON A `report` FRAME.
 *
 * `payroll-register` and `payroll-payslip` are REAL names in
 * authz.KnownReportNames — migration 0035 refuses all five payroll report names
 * to the seeded Data Entry level, and both genuinely PRINT — so a Gateway label
 * that fell through to `canonicalReportName` would push
 * `{screen: "report", reportName: "Payroll Register"}`. That frame resolves to
 * `UnwiredReportScreen`, which politely announces that a built, ACL-gated,
 * printable report "is not available in this build".
 *
 * That failure is INVISIBLE to a test that only checks the label is offered, and
 * it is the exact mistake the three cost screens' labels were written up to
 * prevent — so it is asserted as a property here (no payroll frame is ever a
 * report frame) rather than as a list of the four labels that happen to exist
 * today.
 */

const keymap = resolveKeymap("tallyprime");

/** Fresh machine, drilled from the Gateway by selecting `label` and Enter. */
function openFromGateway(label: string) {
  const actor = createActor(navMachine, {
    input: { startScreen: "gateway", keymapProfile: "tallyprime" },
  }).start();
  actor.send({ type: "SELECT", menuItem: label });
  actor.send({ type: "KEY", combo: "Enter" });
  return actor;
}

describe("every payroll door lands on its own screen, never on a report frame", () => {
  const doors: [string, string][] = [
    [EMPLOYEES_LABEL, "employees"],
    [PAY_RUN_LABEL, "payroll-run"],
    [PAYROLL_REGISTER_LABEL, "payroll-register"],
    [FORM_138_LABEL, "payroll-form-138"],
  ];

  for (const [label, screen] of doors) {
    it(`"${label}" opens the ${screen} screen`, () => {
      const actor = openFromGateway(label);
      const snap = projectSnapshot(actor.getSnapshot().context);
      expect(snap.screen).toBe(screen);
      // ⛔ THE ASSERTION THAT MATTERS. A `report` frame here would render
      // UnwiredReportScreen over a built report — plausible-looking, and wrong
      // all the way to the moment somebody needs the figures.
      expect(snap.screen).not.toBe("report");
      expect(snap.reportName).toBeUndefined();
    });
  }

  it("a payroll label is HIGHLIGHTABLE on the Gateway before Enter", () => {
    // The other half of the door. A label the SELECT branch does not recognise
    // is never highlighted, so Enter has nothing to act on and the entry is dead
    // — silently, because the menu still lists it.
    for (const [label] of doors) {
      const actor = createActor(navMachine, {
    input: { startScreen: "gateway", keymapProfile: "tallyprime" },
  }).start();
      actor.send({ type: "SELECT", menuItem: label });
      expect(projectSnapshot(actor.getSnapshot().context).selectedItem).toBe(
        label,
      );
    }
  });
});

describe("Ctrl+F4 opens the pay run from anywhere the global keymap reaches", () => {
  it("pushes the pay-run SCREEN and not a voucher frame", () => {
    // ⛔ A VOUCHER FRAME WOULD NOT "PARTLY WORK". VoucherScreen resolves its
    // layout through configFor(voucherType), which has no payroll entry, so the
    // key would take the operator to an undefined config. And a pay run has no
    // particulars to key at all — its lines are computed from the masters.
    const actor = createActor(navMachine, {
    input: { startScreen: "gateway", keymapProfile: "tallyprime" },
  }).start();
    actor.send({ type: "KEY", combo: "Ctrl+F4" });
    const snap = projectSnapshot(actor.getSnapshot().context);
    expect(snap.screen).toBe("payroll-run");
    expect(snap.voucherType).toBeUndefined();
  });

  it("does not stack a second frame when pressed on the screen it opens", () => {
    const actor = createActor(navMachine, {
    input: { startScreen: "gateway", keymapProfile: "tallyprime" },
  }).start();
    actor.send({ type: "KEY", combo: "Ctrl+F4" });
    const depth = projectSnapshot(actor.getSnapshot().context).screenStackDepth;
    actor.send({ type: "KEY", combo: "Ctrl+F4" });
    expect(
      projectSnapshot(actor.getSnapshot().context).screenStackDepth,
    ).toBe(depth);
  });

  it("the Gateway entry and the key land on the SAME frame", () => {
    // A door and a key on one destination must agree. When they do not, an
    // operator's Esc history depends on which one they used, and the two frames
    // drift apart the first time either grows a field.
    const byKey = createActor(navMachine, {
    input: { startScreen: "gateway", keymapProfile: "tallyprime" },
  }).start();
    byKey.send({ type: "KEY", combo: "Ctrl+F4" });
    const byLabel = openFromGateway(PAY_RUN_LABEL);
    expect(projectSnapshot(byKey.getSnapshot().context).screen).toBe(
      projectSnapshot(byLabel.getSnapshot().context).screen,
    );
  });
});

describe("the register drills into a payslip, carrying the month", () => {
  it("Enter on a highlighted row opens the payslip WITH the month", () => {
    const actor = createActor(navMachine, {
    input: { startScreen: "gateway", keymapProfile: "tallyprime" },
  }).start();
    actor.send({ type: "SELECT", menuItem: PAYROLL_REGISTER_LABEL });
    actor.send({ type: "KEY", combo: "Enter" });
    // Stand in for what the screen does when it loads a month.
    actor.send({ type: "CONFIG", patch: { wageMonth: "2026-07" } });
    actor.send({ type: "SELECT", menuItem: `${EMPLOYEE_ROW_PREFIX}E-0007` });
    actor.send({ type: "KEY", combo: "Enter" });

    const snap = projectSnapshot(actor.getSnapshot().context);
    expect(snap.screen).toBe("payroll-payslip");
    expect(snap.detailOf).toBe(`${EMPLOYEE_ROW_PREFIX}E-0007`);
    // ⛔ THE MONTH TRAVELS. Without it the payslip lands on "type a month", and
    // an operator who re-keys it slightly differently reads one person's July
    // payslip under a register for June, with nothing on either page saying so.
    expect(snap.config?.wageMonth).toBe("2026-07");
  });

  it("Enter with NO row highlighted does nothing at all", () => {
    // A frame for "some employee" is worse than no frame: this screen would then
    // resolve whichever row it met first, and a payslip is one individual's whole
    // pay under their own name.
    const actor = openFromGateway(PAYROLL_REGISTER_LABEL);
    const before = projectSnapshot(actor.getSnapshot().context);
    actor.send({ type: "KEY", combo: "Enter" });
    const after = projectSnapshot(actor.getSnapshot().context);
    expect(after.screen).toBe(before.screen);
    expect(after.screenStackDepth).toBe(before.screenStackDepth);
  });

  it("the drill carries the employee CODE and never an id", () => {
    // The machine is pure and has no database. A uuid on a frame would be the
    // first step to a screen asking an operator for one.
    const actor = createActor(navMachine, {
    input: { startScreen: "gateway", keymapProfile: "tallyprime" },
  }).start();
    actor.send({ type: "SELECT", menuItem: PAYROLL_REGISTER_LABEL });
    actor.send({ type: "KEY", combo: "Enter" });
    actor.send({ type: "SELECT", menuItem: `${EMPLOYEE_ROW_PREFIX}E-0007` });
    actor.send({ type: "KEY", combo: "Enter" });
    const snap = projectSnapshot(actor.getSnapshot().context);
    expect(snap.detailOf).not.toMatch(
      /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i,
    );
  });
});

describe("the payroll screens' keys are bound, labelled and advertised", () => {
  const formScreens = ["employees", "payroll-run", "payroll-form-138"] as const;
  const allScreens = [
    ...formScreens,
    "payroll-register",
    "payroll-payslip",
  ] as const;

  it("holds the voucher keys back on every screen that carries a FORM", () => {
    // ⛔ AN UNBOUND KEY IS NOT INERT HERE. The global table sends F5 to a Payment
    // voucher and F9 to a Purchase, so a screen with a half-keyed employee on it
    // and no override gets a voucher form pushed over it — and the frame holding
    // the operator's work goes with it. That is not a missing feature but a
    // destructive one.
    for (const screen of formScreens) {
      for (const combo of ["F4", "F5", "F6", "F7", "F8", "F9"]) {
        const action = resolveAction(keymap, screen, combo);
        expect(action?.kind, `${combo} on ${screen}`).toBe("unavailable");
      }
    }
  });

  it("leaves the voucher keys LIVE on the two readers", () => {
    // The other side of the same decision, and it is a decision: there is no form
    // on the register or a payslip to lose, and an operator reading a payslip who
    // presses F5 to key the payment that pays it should get a Payment voucher.
    // A holdback there would be friction invented by us.
    for (const screen of ["payroll-register", "payroll-payslip"] as const) {
      const action = resolveAction(keymap, screen, "F5");
      expect(action?.kind, `F5 on ${screen}`).toBe("voucher");
    }
  });

  it("answers F2 with a REASON on every payroll screen", () => {
    // F2 would otherwise read as "Date" from the global table, on screens whose
    // dimension is a WAGE MONTH typed on their own face. The TDS Register and the
    // Stock Summary both once had the opposite bug — a dialog telling them their
    // dates did nothing while both were reading them — so the rule here is that
    // F2 must SAY something, never silently do nothing.
    for (const screen of allScreens) {
      const action = resolveAction(keymap, screen, "F2");
      expect(action?.kind, `F2 on ${screen}`).toBe("unavailable");
      if (action?.kind === "unavailable") {
        expect(action.note.length).toBeGreaterThan(20);
      }
    }
  });

  it("gives every payroll screen its OWN button bar", () => {
    // Without a case in buttonBarFor a screen falls through to GATEWAY_BAR, WHICH
    // NO TEST CATCHES: the bar would advertise F3 Company and F11 Features over a
    // half-keyed employee and say nothing about Alt+C — or about Alt+S, the only
    // key that makes somebody payable at all.
    const gateway = buttonBarFor("gateway").map((i) => i.key).join(",");
    for (const screen of allScreens) {
      const bar = buttonBarFor(screen).map((i) => i.key).join(",");
      expect(bar, `${screen} falls through to the Gateway bar`).not.toBe(gateway);
    }
  });

  it("advertises Alt+S on BOTH screens that own it, and binds it there", () => {
    // ⛔ ALT+S ON THE EMPLOYEE MASTER IS THE KEY THAT MAKES A PERSON PAYABLE, and
    // a bar that advertised only Alt+C would be advertising half a task: an
    // employee with no salary structure and no election makes cored refuse the
    // WHOLE pay run, days later, naming them.
    for (const screen of ["employees", "payroll-register"] as const) {
      expect(resolveAction(keymap, screen, "Alt+S")).toBeTruthy();
      expect(
        buttonBarFor(screen).some((i) => i.key === "Alt+S"),
        `${screen}'s bar does not advertise Alt+S`,
      ).toBe(true);
    }
    expect(resolveAction(keymap, "employees", "Alt+S")?.kind).toBe(
      "setEmployeePay",
    );
    expect(resolveAction(keymap, "payroll-register", "Alt+S")?.kind).toBe(
      "payrollStatutoryFiles",
    );
  });

  it("labels every payroll action, so none is a blank line in F1 Help", () => {
    for (const action of [
      resolveAction(keymap, "gateway", "Ctrl+F4"),
      resolveAction(keymap, "employees", "Alt+C"),
      resolveAction(keymap, "employees", "Alt+S"),
      resolveAction(keymap, "payroll-register", "Alt+S"),
    ]) {
      expect(action).toBeTruthy();
      expect(actionLabel(action!).length).toBeGreaterThan(0);
    }
  });
});
