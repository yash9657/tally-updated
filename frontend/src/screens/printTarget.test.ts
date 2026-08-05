import { describe, it, expect } from "vitest";
import { createActor } from "xstate";
import { navMachine, projectSnapshot } from "../keystroke/navMachine";
import type { NavSnapshot } from "../keystroke/navMachine";
import { resolveAction, resolveKeymap, actionLabel } from "../keystroke/keymaps/tallyprime";
import type { VoucherType } from "../keystroke/types";
import { resolvePrintTarget, PRINTABLE_VOUCHER_CLASSES } from "./printTarget";

/**
 * The PRINT-REACHABILITY gate. Invariants I-1 through I-5, I-10 and I-12 of
 * docs/design/t1t4-ui-model.md.
 *
 * T1 shipped a printing pipeline with no key anywhere in the client, and the
 * handoff calls that a parity gap rather than a nicety: a keystroke-compatible
 * product whose operator cannot print the invoice they just keyed has not
 * replaced Tally for them. These tests pin the KEY and the DECISION; the fetch
 * itself is the screen's business.
 */

const keymap = resolveKeymap("tallyprime");

/** Every ScreenKind, so the coverage assertions cannot silently miss one. */
const ALL_SCREENS: NavSnapshot["screen"][] = [
  "gateway",
  "menu",
  "voucher",
  "report",
  "report-detail",
  "goto-overlay",
  "master-create",
  "bill-inbox",
  "query",
  "company-select",
  "members",
  "edit-log",
  "help",
  "features",
  "bank-reconciliation",
  // No printed layout exists for the challan register, so Ctrl+P resolves to a
  // SENTENCE and makes no request — but the key must still be BOUND here, which
  // is what this list checks.
  "tds-challans",
  // The three T2 cost screens. None of them prints — the two summaries are in
  // handler_print_reports.go's deferred set and answer 501, and a list of masters
  // is not a report at all — so Ctrl+P resolves to a SENTENCE on each. The key
  // must still be BOUND on all three, which is what this list checks; the
  // sentences themselves are asserted below.
  "cost-centres",
  "cost-centre-summary",
  "cost-category-summary",
];

/** A bare snapshot for a screen, with only the fields the resolver reads. */
function snap(over: Partial<NavSnapshot>): NavSnapshot {
  return {
    screen: "gateway",
    screenStackDepth: 1,
    canReturnToVoucher: false,
    ...over,
  };
}

describe("the print key is bound at all (I-1)", () => {
  // Ctrl+P was the ONE documented TallyPrime combo missing from the keymap, and
  // its absence was not inert: AppShell returns early on an unresolved combo, so
  // the key fell through to the BROWSER and printed a screenshot of the DOM —
  // a dark terminal emulation with no Rule 46 particulars and no GSTIN. See
  // docs/design/t1t4-ui-model.md §0.1.
  it("binds Ctrl+P on every screen, so it never reaches the browser", () => {
    for (const screen of ALL_SCREENS) {
      const action = resolveAction(keymap, screen, "Ctrl+P");
      expect(action, `Ctrl+P is unbound on ${screen}`).toBeTruthy();
      expect(action?.kind).toBe("print");
    }
  });

  it("binds Alt+P too, because ERP 9 used it and Prime still does", () => {
    for (const screen of ALL_SCREENS) {
      expect(
        resolveAction(keymap, screen, "Alt+P"),
        `Alt+P is unbound on ${screen}`,
      ).toBeTruthy();
    }
  });
});

describe("Ctrl+P and Alt+P do not converge on one message (I-12)", () => {
  // RESEARCH.md:2165 — ERP 9: Alt+P. TallyPrime: "Ctrl+P (voucher) / Alt+P
  // (print menu)". They are DIFFERENT Tally actions, and we have no options
  // menu, so Alt+P must say it printed with defaults rather than impersonating
  // Ctrl+P. An operator who believes they visited an options screen has chosen
  // settings they never saw.
  it("Alt+P names the missing options menu and Ctrl+P does not", () => {
    const direct = resolveAction(keymap, "voucher", "Ctrl+P");
    const viaMenu = resolveAction(keymap, "voucher", "Alt+P");

    expect(direct).toEqual({ kind: "print" });
    expect(viaMenu).toEqual({ kind: "print", viaOptionsMenu: true });

    const directLabel = actionLabel(direct!);
    const menuLabel = actionLabel(viaMenu!);
    expect(menuLabel).not.toBe(directLabel);
    expect(menuLabel.toLowerCase()).toContain("no options menu");
    expect(directLabel.toLowerCase()).not.toContain("options menu");
  });
});

describe("which voucher classes print (I-3)", () => {
  // A duplicate of a SERVER decision (printingdb.docKindForClass). The duplicate
  // is deliberate — the client must refuse locally rather than round-trip to a
  // 404 it cannot explain — but it must not drift, so the list is asserted
  // exactly. The Go side asserts the same six from the other direction.
  it("is exactly the six the server produces a document for", () => {
    expect([...PRINTABLE_VOUCHER_CLASSES].sort()).toEqual(
      ["credit-note", "debit-note", "delivery-note", "payment", "receipt", "sales"].sort(),
    );
  });

  it("resolves a saved printable voucher to its route", () => {
    const target = resolvePrintTarget(
      snap({ screen: "voucher", voucherType: "sales", mode: "alter" }),
      { voucherID: "11111111-2222-3333-4444-555555555555" },
    );
    expect(target.kind).toBe("voucher");
    if (target.kind !== "voucher") return;
    expect(target.voucherClass).toBe("sales");
    expect(target.voucherID).toBe("11111111-2222-3333-4444-555555555555");
  });
});

describe("every refusal names its reason (I-4)", () => {
  // "Printing failed" sends an operator to support. "A purchase bill is the
  // supplier's document" tells them they are not missing a feature.
  //
  // Six here, not the server's seven: `payroll` is a voucher CLASS on the server
  // but not a member of the client's VoucherType union, because there is no
  // payroll entry screen in this build (payroll is server-only). So it is
  // unreachable from a frame and cannot be printed from one.
  const NON_PRINTABLE: VoucherType[] = [
    "purchase",
    "contra",
    "journal",
    "receipt-note",
    "stock-journal",
    "physical-stock",
  ];

  for (const cls of NON_PRINTABLE) {
    it(`${cls} is refused with a reason specific to it`, () => {
      const target = resolvePrintTarget(
        snap({ screen: "voucher", voucherType: cls, mode: "alter" }),
        { voucherID: "11111111-2222-3333-4444-555555555555" },
      );
      expect(target.kind).toBe("refusal");
      if (target.kind !== "refusal") return;
      // Not a generic failure, and long enough to actually be an explanation.
      expect(target.reason.length).toBeGreaterThan(30);
      expect(target.reason.toLowerCase()).not.toContain("failed");
    });
  }

  it("the reasons are DISTINCT, not one message with the class interpolated", () => {
    const reasons = new Set(
      NON_PRINTABLE.map((cls) => {
        const t = resolvePrintTarget(
          snap({ screen: "voucher", voucherType: cls, mode: "alter" }),
          { voucherID: "id" },
        );
        return t.kind === "refusal" ? t.reason : "";
      }),
    );
    // A purchase is refused for a different REASON than a stock journal, and
    // flattening them would lose the only useful part of the message.
    expect(reasons.size).toBeGreaterThanOrEqual(4);
  });

  it("refuses a non-printable class even though it IS saved", () => {
    // The class check must come BEFORE the saved check: a Contra does not become
    // printable by being saved, and "save it first" would be a lie acted upon.
    const target = resolvePrintTarget(
      snap({ screen: "voucher", voucherType: "contra", mode: "alter" }),
      { voucherID: "a-real-id" },
    );
    expect(target.kind).toBe("refusal");
    if (target.kind !== "refusal") return;
    expect(target.reason.toLowerCase()).not.toContain("save");
  });
});

describe("an unsaved voucher cannot print (I-5)", () => {
  it("refuses, and tells the operator to press Ctrl+A first", () => {
    const target = resolvePrintTarget(
      snap({ screen: "voucher", voucherType: "sales", mode: "create" }),
      { voucherID: null },
    );
    expect(target.kind).toBe("refusal");
    if (target.kind !== "refusal") return;
    expect(target.reason).toContain("Ctrl+A");
  });

  it("prints a voucher JUST saved in create mode — the whole point (§2.1)", () => {
    // The handoff's §3.1 complaint is that an operator cannot print the invoice
    // from the screen they keyed it on. The create response carries voucher_id,
    // so requiring an alteration would have left that complaint standing.
    const target = resolvePrintTarget(
      snap({ screen: "voucher", voucherType: "sales", mode: "create" }),
      { voucherID: "fresh-id", justSaved: true },
    );
    expect(target.kind).toBe("voucher");
    if (target.kind !== "voucher") return;
    expect(target.label).toContain("just saved");
  });
});

describe("reports (I-3, and the drill-down rule)", () => {
  it("maps every report this app can display to a printable slug", () => {
    for (const [name, slug] of [
      ["Balance Sheet", "balance-sheet"],
      ["Profit & Loss A/c", "profit-and-loss"],
      ["Trial Balance", "trial-balance"],
      ["Day Book", "day-book"],
      ["Outstandings", "outstandings"],
      ["Stock Summary", "stock-summary"],
    ] as const) {
      const target = resolvePrintTarget(snap({ screen: "report", reportName: name }));
      expect(target.kind, `${name} did not resolve`).toBe("report");
      if (target.kind !== "report") continue;
      expect(target.slug).toBe(slug);
    }
  });

  it("a drill-down prints its PARENT report, and says so", () => {
    const target = resolvePrintTarget(
      snap({
        screen: "report-detail",
        parentReportName: "Outstandings",
        detailOf: "ledger:Acme Traders",
      }),
    );
    expect(target.kind).toBe("report");
    if (target.kind !== "report") return;
    expect(target.slug).toBe("outstandings");
    // An operator who drilled into one party and got the whole statement must be
    // told why, or they will think the drill was ignored.
    expect(target.label.toLowerCase()).toContain("whole statement");
  });

  it("a display-only report is refused with the 501 reason, not a blank page", () => {
    const target = resolvePrintTarget(
      snap({ screen: "report", reportName: "Cash Flow" }),
    );
    expect(target.kind).toBe("refusal");
    if (target.kind !== "refusal") return;
    expect(target.reason).toContain("501");
  });
});

describe("the Bank Reconciliation Statement prints, for ONE account (I-10)", () => {
  // Memo §1.5. The route shape was the blocker — /print/reports/{name} carried
  // no ledger id and a BRS is always for one bank account. §3.4 took decision
  // (c): ?ledger_id= for one, ?all=true for every account.
  //
  // The refusal survives for the case it was always really about: an operator
  // who has not chosen an account. It is what stops a future well-meaning change
  // from quietly defaulting to the first bank.
  it("refuses when no account is selected, rather than guessing one", () => {
    const target = resolvePrintTarget(snap({ screen: "bank-reconciliation" }));
    expect(target.kind).toBe("refusal");
    if (target.kind !== "refusal") return;
    expect(target.reason).toMatch(/select a bank account/i);
  });

  it("resolves to the SELECTED account's statement", () => {
    const target = resolvePrintTarget(snap({ screen: "bank-reconciliation" }), {
      voucherID: null,
      bankLedgerID: "bank-42",
    });
    expect(target.kind).toBe("report");
    if (target.kind !== "report") return;
    expect(target.slug).toBe("bank-reconciliation");
    // The scope is the point. A report target for this slug with no query is a
    // 400 from the server, and one with the wrong id is a 200 for the wrong bank.
    expect(target.query).toEqual({ ledger_id: "bank-42" });
  });

  it("carries a DIFFERENT id when a different account is selected", () => {
    // The control for the test above: asserting one id alone would pass for an
    // implementation that hard-codes it.
    const target = resolvePrintTarget(snap({ screen: "bank-reconciliation" }), {
      voucherID: null,
      bankLedgerID: "bank-99",
    });
    if (target.kind !== "report") throw new Error("expected a report target");
    expect(target.query).toEqual({ ledger_id: "bank-99" });
  });
});

describe("the cost screens refuse to print, each for its own reason (I-4)", () => {
  // The three refusals must not collapse into one sentence with the screen name
  // interpolated: the centre summary cannot print because the ROUTE cannot carry
  // an axis, the category summary because the LAYOUT is unwritten, and the masters
  // screen because it is not a report. Those are three different answers to "will
  // this ever print", and the middle one is the only one that is just waiting.
  const COST_SCREENS: NavSnapshot["screen"][] = [
    "cost-centre-summary",
    "cost-category-summary",
    "cost-centres",
  ];

  for (const screen of COST_SCREENS) {
    it(`${screen} is refused with a reason specific to it`, () => {
      const target = resolvePrintTarget(snap({ screen }));
      expect(target.kind).toBe("refusal");
      if (target.kind !== "refusal") return;
      expect(target.reason.length).toBeGreaterThan(60);
      // Not the generic fallthrough, which says nothing an operator can act on.
      expect(target.reason).not.toBe("Nothing on this screen can be printed.");
      expect(target.reason.toLowerCase()).not.toContain("failed");
    });
  }

  it("the three reasons are DISTINCT", () => {
    const reasons = new Set(
      COST_SCREENS.map((screen) => {
        const t = resolvePrintTarget(snap({ screen }));
        return t.kind === "refusal" ? t.reason : "";
      }),
    );
    expect(reasons.size).toBe(3);
  });

  it("names the 501 for the two reports, and the AXIS for the one that has one", () => {
    const centre = resolvePrintTarget(snap({ screen: "cost-centre-summary" }));
    const category = resolvePrintTarget(snap({ screen: "cost-category-summary" }));
    if (centre.kind !== "refusal" || category.kind !== "refusal") {
      throw new Error("expected both cost summaries to be refused");
    }
    // Both are mapped-and-deferred on the server, so 501 is the honest number to
    // quote — the same treatment "Cash Flow" gets above.
    expect(centre.reason).toContain("501");
    expect(category.reason).toContain("501");
    // And the centre summary's refusal names the axis problem, which is the reason
    // it is a refusal here rather than a slug mapped through to the server.
    expect(centre.reason).toMatch(/ONE cost category/);
    // The category summary's refusal carries the rule a printed copy would have to
    // carry: its rows may never be added together.
    expect(category.reason).toMatch(/NEVER be added together/i);
  });

  it("no cost screen resolves to a report slug (nothing is fetched)", () => {
    // I-5 in this feature's direction: a refusal that still hit the network would
    // be a guaranteed 501 in the server's log for every Ctrl+P.
    for (const screen of COST_SCREENS) {
      expect(resolvePrintTarget(snap({ screen })).kind).not.toBe("report");
    }
  });
});

describe("print never navigates, and reconcile does (the machine)", () => {
  function drive(events: { type: string; combo?: string; menuItem?: string }[]) {
    const actor = createActor(navMachine, {
      input: { startScreen: "gateway", keymapProfile: "tallyprime" },
    });
    actor.start();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    for (const ev of events) actor.send(ev as any);
    const s = projectSnapshot(actor.getSnapshot().context);
    actor.stop();
    return s;
  }

  it("Ctrl+P leaves the operator on the voucher they printed", () => {
    const before = drive([{ type: "KEY", combo: "F8" }]);
    const after = drive([
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "Ctrl+P" },
    ]);
    expect(after.screen).toBe("voucher");
    expect(after.voucherType).toBe("sales");
    expect(after.screenStackDepth).toBe(before.screenStackDepth);
  });

  it("the Gateway entry opens the reconciliation screen in picker mode", () => {
    const s = drive([
      { type: "SELECT", menuItem: "Bank Reconciliation" },
      { type: "KEY", combo: "Enter" },
    ]);
    expect(s.screen).toBe("bank-reconciliation");
    // No ledger chosen: the screen must offer a picker.
    expect(s.detailOf).toBeUndefined();
  });

  it("the Gateway entry is matched BEFORE the report-name fallback", () => {
    // "bank-reconciliation" is a real name in authz.KnownReportNames, so a
    // fallback-matched frame would look plausible all the way to a 501 from the
    // print route. The frame must be the SCREEN, not a report called
    // "Bank Reconciliation".
    const s = drive([
      { type: "SELECT", menuItem: "Bank Reconciliation" },
      { type: "KEY", combo: "Enter" },
    ]);
    expect(s.screen).toBe("bank-reconciliation");
    expect(s.reportName).toBeUndefined();
  });

  it("the Gateway entry twice does not stack two statements", () => {
    const once = drive([
      { type: "SELECT", menuItem: "Bank Reconciliation" },
      { type: "KEY", combo: "Enter" },
    ]);
    const twice = drive([
      { type: "SELECT", menuItem: "Bank Reconciliation" },
      { type: "KEY", combo: "Enter" },
      { type: "KEY", combo: "F4" },
    ]);
    expect(twice.screenStackDepth).toBe(once.screenStackDepth);
  });

  it("F5 on the statement does NOT open a Payment voucher", () => {
    // The global F5 is the Payment voucher. Without the screen override it would
    // open one ON TOP of the statement — not a missing feature but a wrong one,
    // which is exactly what the keymap's coverage rule exists to prevent.
    const s = drive([
      { type: "SELECT", menuItem: "Bank Reconciliation" },
      { type: "KEY", combo: "Enter" },
      { type: "KEY", combo: "F5" },
    ]);
    expect(s.screen).toBe("bank-reconciliation");
    expect(s.voucherType).toBeUndefined();
  });

  it("the statement's unbuilt keys answer instead of doing something wrong", () => {
    // RESEARCH.md:879 lists F5/F8/F9/Alt+F8/Alt+S inside reconciliation. All
    // belong to statement import (T4 slice 2). Each must be bound to an explicit
    // "not built" — F9 in particular, whose global binding is a PURCHASE voucher.
    for (const combo of ["F5", "F8", "F9", "Alt+F8", "Alt+S"]) {
      const action = resolveAction(keymap, "bank-reconciliation", combo);
      expect(action, `${combo} is unbound on the statement`).toBeTruthy();
      expect(
        action?.kind,
        `${combo} on the statement resolves to ${action?.kind}, which is not a refusal`,
      ).toBe("unavailable");
    }
  });

  it("F4 picks the bank account without moving the stack", () => {
    // RESEARCH.md:879 — "F4 (select bank ledger)". The screen owns the picker.
    const action = resolveAction(keymap, "bank-reconciliation", "F4");
    expect(action).toEqual({ kind: "bankReconciliation" });
  });
});
