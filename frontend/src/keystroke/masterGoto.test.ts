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
  currency: {
    // T8. Same guard as the stock group below: a currency master with no
    // keystroke that reaches it is a form that exists and cannot be opened.
    how: "Alt+G > Create Currency",
    events: [
      { type: "KEY", combo: "Alt+G" },
      { type: "SELECT", menuItem: "Create Currency" },
      { type: "KEY", combo: "Enter" },
    ],
  },
  "stock-group": {
    // T10. ⚠️ The table refused to compile the moment "stock-group" joined the
    // union, which is exactly what it is for: a MasterKind with no keystroke that
    // reaches it is a form that exists and cannot be opened — the whole of
    // register row 20, for five kinds at once.
    how: "Alt+G > Create Stock Group",
    events: [
      { type: "KEY", combo: "Alt+G" },
      { type: "SELECT", menuItem: "Create Stock Group" },
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
    // ⛔⛔ THIS LOOP USED TO CONTAIN `entry === "Vouchers" ||`, AND THAT STRING
    // COMPARISON IS WHAT LET D80 SHIP. It asserted that the label was on a list
    // of things believed routable — not that the MACHINE routes it — so the one
    // entry in the picker that landed on "not available in this build" was
    // certified as reachable by the very test written to make reachability
    // structural. `masterKindForGotoLabel` and `REPORT_NAMES` are the same shape
    // of claim, but those two really are the machine's own routing tables; the
    // Vouchers arm had no table, so it needed a DRIVE, and now it has one.
    for (const entry of GOTO_ENTRIES) {
      const routed =
        masterKindForGotoLabel(entry) !== undefined ||
        (REPORT_NAMES as readonly string[]).includes(entry);
      if (routed) continue;
      // Not in either table: the machine must be DRIVEN to prove it lands
      // somewhere real. "Somewhere real" excludes a report frame, because that
      // is precisely the accidental landing this row was about.
      const snap = drive([
        { type: "KEY", combo: "Alt+G" },
        { type: "SELECT", menuItem: entry },
        { type: "KEY", combo: "Enter" },
      ]);
      expect(snap.screen, `${entry} is offered but routed nowhere`).not.toBe(
        "report",
      );
      expect(snap.reportName, `${entry} landed on a report frame`).toBeUndefined();
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

/**
 * ===========================================================================
 * ⛔⛔ REGISTER ROW D80 — `Alt+G > Vouchers` PUSHED THE **UNWIRED** SCREEN, AND
 * IT IS THE FIRST ENTRY THE PICKER EVER OFFERED.
 * ===========================================================================
 *
 * `GOTO_ENTRIES` is literally `["Vouchers", ...masters, ...REPORT_NAMES]`, so
 * "Vouchers" has been at the top of that list since the picker existed — and it
 * was the only entry that did not work. `canonicalReportName` returns undefined
 * for it ON PURPOSE (a voucher is not a report), the `??` in
 * `resolveGotoSelection` then fell back to the RAW LABEL, and the operator got
 * `{screen:"report", reportName:"Vouchers"}` — which `defaultScreens` maps to
 * `UnwiredReportScreen`, a screen that politely announces vouchers are "not
 * available in this build".
 *
 * ⛔ THE GATEWAY'S OWN DRILL HAS ALWAYS BEEN CORRECT for the identical label
 * (`newVoucherFrame(DEFAULT_LAST_VOUCHER)`), which is why this never looked like
 * a missing feature: the door most people use worked.
 *
 * ⛔ THE DESTINATION IS DOCUMENTED, NOT CHOSEN. `RESEARCH.md:903` — *"Payroll
 * voucher: Alt+G > **Create Voucher** > Ctrl+F4"* — so Alt+G's voucher door is a
 * create-voucher FORM, not the Day Book.
 */
describe("⛔ D80: Alt+G > Vouchers opens a voucher, not the Unwired screen", () => {
  const openVouchers = (combo: "Alt+G" | "Ctrl+G") => [
    { type: "KEY", combo } as const,
    { type: "SELECT", menuItem: "Vouchers" } as const,
    { type: "KEY", combo: "Enter" } as const,
  ];

  it("⛔ lands on a real Sales voucher entry form with the cursor on Date", () => {
    const snap = drive([...openVouchers("Alt+G")]);
    expect(snap.screen).toBe("voucher");
    expect(snap.voucherType).toBe("sales");
    expect(snap.mode).toBe("create");
    // The cursor has to be somewhere, or the operator's first keystroke goes
    // nowhere — the same reason the master arm above sets `field: "name"`.
    expect(snap.field).toBe("date");
    // ⛔ LOAD-BEARING. A report frame named "Vouchers" is exactly what existed
    // before, and `screen === "voucher"` alone would not have caught a build
    // that pushed BOTH.
    expect(snap.reportName).toBeUndefined();
  });

  it("⛔ and Esc returns to where it came from", () => {
    const snap = drive([...openVouchers("Alt+G"), { type: "KEY", combo: "Esc" }]);
    expect(snap.screen).toBe("gateway");
  });

  it("⛔ THE `replaced` DECISION: Ctrl+G > Vouchers stays returnable once anything is pushed over it", () => {
    // Every other arm of `resolveGotoSelection` sets `replaced` under a Switch
    // To. This one deliberately does not, and THIS is the assertion that makes
    // that a decision rather than an oversight.
    //
    // `replaced` is read in exactly one place — `canReturnToVoucher`, over the
    // frames BELOW the top. So on the depth-2 stack the picker produces it is
    // unobservable, and an assertion there would be vacuous. Push a
    // master-create over the voucher (Alt+C, the real keystroke) and it becomes
    // observable: with `replaced` set, the projection reports "you cannot Esc
    // back to a voucher" while Esc pops unconditionally and does exactly that.
    const snap = drive([
      ...openVouchers("Ctrl+G"),
      { type: "KEY", combo: "Alt+C" },
    ]);
    expect(snap.screen).toBe("master-create");
    expect(snap.canReturnToVoucher).toBe(true);

    // …and the projection is telling the truth: Esc really does return.
    const back = drive([
      ...openVouchers("Ctrl+G"),
      { type: "KEY", combo: "Alt+C" },
      { type: "KEY", combo: "Esc" },
    ]);
    expect(back.screen).toBe("voucher");
    expect(back.voucherType).toBe("sales");
  });

  it("⛔ CONTROL: a REPORT entry still resolves to a report frame", () => {
    // Without this, an arm that swallowed every label into a voucher frame would
    // pass every assertion above and break the whole picker.
    const snap = drive([
      { type: "KEY", combo: "Alt+G" },
      { type: "SELECT", menuItem: "Balance Sheet" },
      { type: "KEY", combo: "Enter" },
    ]);
    expect(snap.screen).toBe("report");
    expect(snap.reportName).toBe("Balance Sheet");
  });

  it("⛔ CONTROL: `canonicalReportName`'s undefined arm for \"vouchers\" is UNTOUCHED", () => {
    // The fix must not be made by teaching canonicalReportName to return a
    // report name for "Vouchers" — that would put the voucher door back on the
    // report path with a nicer label. The proof is that the raw-label fallback
    // is no longer reachable for it: nothing named "Vouchers" resolves to a
    // report, in either picker mode.
    for (const combo of ["Alt+G", "Ctrl+G"] as const) {
      for (const label of ["Vouchers", "vouchers", "  VOUCHERS  "]) {
        const snap = drive([
          { type: "KEY", combo },
          { type: "SELECT", menuItem: label },
          { type: "KEY", combo: "Enter" },
        ]);
        expect(snap.screen, `${combo} + ${JSON.stringify(label)}`).toBe("voucher");
      }
    }
  });
});

// ---------------------------------------------------------------------------
// D94 — the payables ageing has a door, and the alias that lied now points at it
// ---------------------------------------------------------------------------

describe("D94 — 'bills payable' reaches the PAYABLES report", () => {
  it("⛔⛔ 'bills payable' no longer opens the RECEIVABLE report", () => {
    // ⛔ THIS ARM IS RED ON THE PREVIOUS COMMIT, and the defect it names had
    // SHIPPED: `REPORT_ALIASES` mapped "bills payable" -> "Outstandings", which is
    // what customers owe US. An operator typing the words for what the company owes
    // was shown the opposite, under a heading that did not say so — the same class
    // of wrong answer L16 deliberately refused to create for "cash flow statement"
    // (§6.5), except this one was already live. It could not be fixed before D94,
    // because there was no payables report to point it at.
    for (const label of ["bills payable", "Bills Payable", "  BILLS PAYABLE  "]) {
      const snap = drive([
        { type: "KEY", combo: "Alt+G" },
        { type: "SELECT", menuItem: label },
        { type: "KEY", combo: "Enter" },
      ]);
      expect(snap.screen, JSON.stringify(label)).toBe("report");
      expect(snap.reportName, JSON.stringify(label)).toBe("Bills Payable");
    }
  });

  it("⛔ CONTROL: the RECEIVABLE aliases still reach Outstandings", () => {
    // Without this, "re-point every alias at the new report" passes the arm above
    // and quietly does the same damage in the other direction.
    for (const label of ["outstandings", "bills receivable", "bills outstanding"]) {
      const snap = drive([
        { type: "KEY", combo: "Alt+G" },
        { type: "SELECT", menuItem: label },
        { type: "KEY", combo: "Enter" },
      ]);
      expect(snap.screen, JSON.stringify(label)).toBe("report");
      expect(snap.reportName, JSON.stringify(label)).toBe("Outstandings");
    }
  });

  it("the other words an operator reaches for also land on the payables book", () => {
    for (const label of ["payables", "accounts payable", "creditors ageing"]) {
      const snap = drive([
        { type: "KEY", combo: "Alt+G" },
        { type: "SELECT", menuItem: label },
        { type: "KEY", combo: "Enter" },
      ]);
      expect(snap.reportName, JSON.stringify(label)).toBe("Bills Payable");
    }
  });
});
